package logprocessor

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
	"github.com/Scorcher/dns-to-route-resolver/internal/metrics"
	"github.com/fsnotify/fsnotify"
)

// LogEntry represents a parsed DNS log entry
type LogEntry struct {
	ClientIP  net.IP
	Domain    string
	QueryType string
}

const (
	defaultRetryInterval = 10 * time.Second
	maxRetries           = 5
)

// Processor processes DNS log entries
type Processor struct {
	cfg       *config.Config
	logger    *log.Logger
	domains   map[string]struct{}
	eventChan chan LogEntry
	done      chan struct{}
	wg        sync.WaitGroup
	metrics   *metrics.Collector
}

// NewProcessor creates a new log processor
func NewProcessor(cfg *config.Config, metrics *metrics.Collector) *Processor {
	// Create a set of monitored domains for faster lookups
	domains := make(map[string]struct{})
	for _, domain := range cfg.Network.MonitoredDomains {
		domains[strings.ToLower(domain)] = struct{}{}
	}

	return &Processor{
		cfg:       cfg,
		logger:    log.GetLogger(),
		domains:   domains,
		eventChan: make(chan LogEntry, 1000), // Buffered channel to prevent blocking
		done:      make(chan struct{}),
		metrics:   metrics,
	}
}

// Start starts the log processor
func (p *Processor) Start() error {
	// If no log file is specified, just log a warning and return
	if p.cfg.DNSLog.Path == "" {
		p.logger.Warn("No log file specified, log processing disabled")
		return nil
	}

	// Check if log file exists
	if _, err := os.Stat(p.cfg.DNSLog.Path); os.IsNotExist(err) {
		p.logger.Warnf("Log file does not exist: %s, will retry", p.cfg.DNSLog.Path)
		return nil
	}

	p.StartInternal()

	p.logger.Info("Log processor started")
	return nil
}

// StartInternal starts the log processor
func (p *Processor) StartInternal() {
	p.metrics.SetDnsLogProcessing(0)

	// Start processing logs
	p.wg.Add(1)
	go p.processLogs()

	// Start file watcher if following is enabled
	if p.cfg.DNSLog.Follow {
		p.wg.Add(1)
		go p.watchLogFile()
	}
}

// Stop stops the log processor
func (p *Processor) Stop() {
	p.StopInternal()
	close(p.eventChan)
}

// StopInternal stops the log processor
func (p *Processor) StopInternal() {
	close(p.done)
	p.wg.Wait()
}

// Events returns a channel that receives parsed log entries
func (p *Processor) Events() <-chan LogEntry {
	return p.eventChan
}

// processLogs processes the log file
func (p *Processor) processLogs() {
	defer p.wg.Done()

	retryCount := 0
	for {
		if retryCount >= maxRetries {
			p.logger.Fatal("Max retries reached for log processing, giving up")
			return
		}

		if !p.processLogsWithRetry() {
			retryCount++
			p.logger.Errorf("Error in log processing, retrying in %v (attempt %d/%d)",
				defaultRetryInterval, retryCount, maxRetries)

			select {
			case <-p.done:
				return
			case <-time.After(defaultRetryInterval):
				continue
			}
		}
		retryCount = 0 // Reset retry counter on successful processing
	}
}

func (p *Processor) processLogsWithRetry() bool {
	file, err := os.Open(p.cfg.DNSLog.Path)
	if err != nil {
		p.logger.Errorf("Failed to open log file: %v", err)
		return false
	}
	defer file.Close()
	defer p.metrics.SetDnsLogProcessing(0)

	p.metrics.SetDnsLogProcessing(1)

	// Seek to the end of the file if we're following
	if p.cfg.DNSLog.Follow {
		_, err := file.Seek(0, io.SeekEnd)
		if err != nil {
			p.logger.Errorf("Failed to seek to end of log file: %v", err)
			return false
		}
	}

	// Create a buffered reader with a timeout
	reader := bufio.NewReader(file)

	// Channel to signal when new data is available
	lines := make(chan string, 100)
	errCh := make(chan error, 1)

	// Goroutine to read lines from file with timeout
	go p.readFile(file, reader, lines, errCh)

	for {
		select {
		case <-p.done:
			return true
		case err := <-errCh:
			if err != nil && err != io.EOF {
				p.logger.Errorf("Error reading log file: %v", err)
			}
			return err == io.EOF
		case line := <-lines:
			entry, err := p.parseLogLine(line)
			if err != nil {
				// Skip invalid log entries
				continue
			}

			// Check if this is a domain we're interested in
			if p.isMonitoredDomain(entry.Domain) {
				select {
				case p.eventChan <- *entry:
					// Event sent successfully
				case <-p.done:
					return true
				}
			}
		}
	}
}

// processLogs processes the log file
func (p *Processor) readFile(file *os.File, reader *bufio.Reader, lines chan string, errCh chan error) {
	for {
		select {
		case <-p.done:
			return
		default:
			// Set a read deadline
			if err := file.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				errCh <- err
				return
			}

			// Try to read a line
			line, isPrefix, err := reader.ReadLine()
			if err != nil {
				if os.IsTimeout(err) {
					// Timeout occurred, check if we should exit
					select {
					case <-p.done:
						return
					default:
						continue
					}
				}
				if err != io.EOF {
					errCh <- err
				}
				return
			}

			// Handle partial lines
			var fullLine []byte
			fullLine = append(fullLine, line...)
			for isPrefix {
				var part []byte
				part, isPrefix, err = reader.ReadLine()
				if err != nil {
					errCh <- err
					return
				}
				fullLine = append(fullLine, part...)
			}

			select {
			case lines <- string(fullLine):
			case <-p.done:
				return
			}
		}
	}
}

// watchLogFile watches the log file for changes
func (p *Processor) watchLogFile() {
	defer p.wg.Done()

	retryCount := 0
	for {
		if retryCount >= maxRetries {
			p.logger.Fatal("Max retries reached for log file watcher, giving up")
			return
		}

		if !p.watchLogFileWithRetry() {
			retryCount++
			p.logger.Errorf("Error in log file watcher, retrying in %v (attempt %d/%d)",
				defaultRetryInterval, retryCount, maxRetries)

			select {
			case <-p.done:
				return
			case <-time.After(defaultRetryInterval):
				continue
			}
		}
		retryCount = 0 // Reset retry counter on successful watch
	}
}

func (p *Processor) watchLogFileWithRetry() bool {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		p.logger.Errorf("Failed to create file watcher: %v", err)
		return false
	}
	defer watcher.Close()

	// Watch the directory containing the log file
	logDir := filepath.Dir(p.cfg.DNSLog.Path)
	if err := watcher.Add(logDir); err != nil {
		p.logger.Errorf("Failed to watch log directory: %v", err)
		return false
	}

	for {
		select {
		case <-p.done:
			return true
		case event, ok := <-watcher.Events:
			if !ok {
				return false
			}

			if event.Op&fsnotify.Create == fsnotify.Create &&
				event.Name == p.cfg.DNSLog.Path {
				p.logger.Info("Detected log file creation, rotating logs...")
				p.handleLogRotation()
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return false
			}
			p.logger.Errorf("File watcher error: %v", err)
			return false
		}
	}
}

func (p *Processor) handleLogRotation() {
	p.StopInternal()
	p.StartInternal()
}

// parseLogLine parses a single line from the dnscrypt-proxy log
func (p *Processor) parseLogLine(line string) (*LogEntry, error) {
	// Example log line format:
	// [2023-01-01 12:34:56] 192.168.1.100 example.com A
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid log line format")
	}

	// Parse client IP
	clientIP := net.ParseIP(parts[2])
	if clientIP == nil {
		return nil, fmt.Errorf("invalid client IP: %s", parts[2])
	}

	// Skip localhost requests to avoid infinite loops
	if clientIP.String() == "127.0.0.1" || clientIP.String() == "::1" {
		return nil, fmt.Errorf("skipping localhost request")
	}

	// Get domain and query type
	domain := parts[3]
	queryType := "A" // Default to A record if not specified
	if len(parts) > 4 {
		queryType = parts[4]
	}

	// We're only interested in A records
	if queryType != "A" {
		return nil, fmt.Errorf("unsupported query type: %s", queryType)
	}

	return &LogEntry{
		ClientIP:  clientIP,
		Domain:    domain,
		QueryType: queryType,
	}, nil
}

// isMonitoredDomain checks if a domain is in the list of monitored domains
func (p *Processor) isMonitoredDomain(domain string) bool {
	// Check exact match first
	if _, ok := p.domains[strings.ToLower(domain)]; ok {
		return true
	}

	// Check subdomains
	for d := range p.domains {
		if strings.HasSuffix("."+domain, "."+d) {
			return true
		}
	}

	return false
}
