package logprocessor

import (
	"bufio"
	"context"
	"errors"
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
	ClientIP   net.IP
	Domain     string
	DomainRule string
	QueryType  string
	Group      string
}

const (
	defaultRetryInterval       = 1 * time.Second
	maxRetries                 = 5
	maxContinuouslyLines       = 100
	readBaseTimeout            = 100 * time.Millisecond
	readEofTimeout             = 1000 * time.Millisecond
	readContinuousLinesTimeout = 50 * time.Millisecond
)

// Processor processes DNS log entries
type Processor struct {
	cfg       *config.Config
	logger    *log.Logger
	domains   map[string]string // domain -> group
	eventChan chan LogEntry
	done      chan struct{}
	wg        sync.WaitGroup
	metrics   *metrics.Collector
	ctx       context.Context
	cancel    context.CancelFunc
	isRotated bool
}

// NewProcessor creates a new log processor
func NewProcessor(cfg *config.Config, metrics *metrics.Collector) *Processor {
	// Create a map of monitored domains to groups for faster lookups
	domains := make(map[string]string)
	for _, group := range cfg.Network.MonitoredDomains {
		for _, domain := range group.Domains {
			domains[strings.ToLower(domain)] = group.Name
		}
	}

	return &Processor{
		cfg:       cfg,
		logger:    log.GetLogger(),
		domains:   domains,
		eventChan: make(chan LogEntry, 1000), // Buffered channel to prevent blocking
		done:      make(chan struct{}),
		metrics:   metrics,
		ctx:       nil,
		cancel:    nil,
		isRotated: false,
	}
}

// Run starts the log processor
func (p *Processor) Run(ctx context.Context) error {
	p.ctx, p.cancel = context.WithCancel(ctx)
	// If no log file is specified, just log a warning and return
	if p.cfg.DNSLog.Path == "" {
		return errors.New("no log file specified, log processing disabled")
	}

	// Check if log file exists
	if _, err := os.Stat(p.cfg.DNSLog.Path); os.IsNotExist(err) {
		return fmt.Errorf("log file does not exist: %s", p.cfg.DNSLog.Path)
	}

	//p.StartInternal()
	p.metrics.SetDnsLogProcessing(0)

	rotateChan := make(chan bool, 1)
	errChan := make(chan error, 1)

	wg := &sync.WaitGroup{}
	// Start processing logs
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.logger.Debug("Start: processLogs")
		if err := p.processLogs(rotateChan); err != nil {
			p.logger.Errorf("Error processing log file: %v", err)
			errChan <- err
		}
	}()

	if p.cfg.DNSLog.Follow {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Start file watcher if following is enabled
			p.logger.Debug("Start: watchLogFile")
			if err := p.watchLogFile(rotateChan); err != nil {
				p.logger.Errorf("Error watching file: %v", err)
				errChan <- err
			}
		}()
	}

	p.logger.Info("Log processor started")

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
	}
	p.logger.Debug("Run: after select")

	p.cancel()
	p.logger.Debug("Run: after p.cancel")

	wg.Wait()
	p.logger.Debug("Run: after Wait")

	close(p.eventChan)
	p.logger.Debug("Run: after eventChannel closed")

	return nil
}

// StopInternal stops the log processor
func (p *Processor) StopInternal() {
	p.logger.Debug("StopInternal: before")
	p.wg.Wait()
	p.logger.Debug("StopInternal: after wait")
}

// Events returns a channel that receives parsed log entries
func (p *Processor) Events() <-chan LogEntry {
	return p.eventChan
}

// processLogs processes the log file
func (p *Processor) processLogs(rotateChan chan bool) error {
	var err error
	var isRotateSignal bool

	retryCount := 0
	for {
		isRotateSignal, err = p.processLogsWithRetry(rotateChan)

		if err == nil {
			retryCount = 0
			if isRotateSignal {
				// continue loop to reopen file again
				continue
			}
			// exit
			return nil
		}

		retryCount++
		p.logger.Errorf("error log processing: %v", err)
		if retryCount >= maxRetries {
			return errors.New("max retries reached for log processing, giving up")
		}
		p.logger.Infof("retrying in %d ms (attempt %d/%d)",
			defaultRetryInterval.Milliseconds(), retryCount, maxRetries)

		select {
		case <-p.ctx.Done():
			return nil
		case <-time.After(defaultRetryInterval):
			continue
		}
	}
}

func (p *Processor) processLogsWithRetry(rotateChan chan bool) (bool, error) {
	p.logger.Debug("Open file: before")
	file, err := os.Open(p.cfg.DNSLog.Path)
	p.logger.Debug("Open file: after")
	if err != nil {
		return false, fmt.Errorf("failed to open log file: %v", err)
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)
	defer p.metrics.SetDnsLogProcessing(0)

	p.metrics.SetDnsLogProcessing(1)

	// Seek to the end of the file if we're following
	if p.cfg.DNSLog.Follow {
		_, err := file.Seek(0, io.SeekEnd)
		if err != nil {
			return false, fmt.Errorf("failed to seek to end of log file: %v", err)
		}
	}

	// Create a buffered reader with a timeout
	reader := bufio.NewReader(file)

	// If not following, read the entire file and process all lines at once
	if !p.cfg.DNSLog.Follow {
		p.logger.Debug("processLogsWithRetry: processEntireFile")
		return p.processEntireFile(reader)
	}

	// If following, read lines continuously with timeout mechanism
	lines := make(chan string, 1)
	errCh := make(chan error, 1)

	p.logger.Debug("processLogsWithRetry: start readFileWithTimeout")
	readCtx, readCancel := context.WithCancel(p.ctx)
	defer readCancel()
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.readFileWithTimeout(readCtx, reader, lines, errCh)
	}()

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Debug("processLogsWithRetry: got ctx.Done")
			readCancel()
			wg.Wait()
			return false, nil
		case <-rotateChan:
			p.logger.Debug("processLogsWithRetry: got rotate signal")
			readCancel()
			wg.Wait()
			return true, nil
		case err := <-errCh:
			readCancel()
			wg.Wait()
			return false, fmt.Errorf("error reading log file: %v", err)
		case line := <-lines:
			p.metrics.IncLinesRead()
			p.logger.Debugf("processLogsWithRetry: got log line \"%s\"", line)
			entry, err := p.parseLogLine(line)
			if err != nil {
				p.logger.Debugf("processLogsWithRetry: parse log line \"%s\" error: %v", line, err)
				// Skip invalid log entries
				continue
			}

			// Check if this is a domain we're interested in
			if entry.Group != "" {
				p.logger.Debugf("processLogsWithRetry: domain \"%s\" with IP %s is in group \"%s\"",
					entry.Domain, entry.ClientIP.String(), entry.Group)
				p.eventChan <- *entry
			} else {
				p.logger.Debugf("processLogsWithRetry: domain \"%s\" out of groups", entry.Domain)
			}

		}
	}
}

// processEntireFile reads the entire file content and processes all lines at once
func (p *Processor) processEntireFile(reader *bufio.Reader) (bool, error) {
	for {
		select {
		case <-p.ctx.Done():
			return false, nil
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					return false, fmt.Errorf("error reading log file: %v", err)
				}
				// EOF reached, processing complete
				return false, nil
			}

			// Trim newline characters
			line = strings.TrimRight(line, "\n\r")

			entry, err := p.parseLogLine(line)
			if err != nil {
				// Skip invalid log entries
				continue
			}

			// Check if this is a domain we're interested in
			if entry.Group != "" {
				p.eventChan <- *entry
			}
		}
	}
}

// readFileWithTimeout read file with timeouts and return lines via channel
func (p *Processor) readFileWithTimeout(ctx context.Context, reader *bufio.Reader, lines chan string, errCh chan error) {
	timer := time.NewTimer(readBaseTimeout)
	defer timer.Stop()

	continuousCount := 0
	for {
		// Try to read a line
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// sleep a little bit and continue reading
				p.logger.Debugf("readFileWithTimeout: EOF, pause reading file for %d ms", readEofTimeout.Milliseconds())
				timer.Reset(readEofTimeout)
			} else {
				errCh <- err
				return
			}
		} else {
			// Trim newline and send line
			line = strings.TrimRight(line, "\n\r")
			p.logger.Debugf("read line: %s", line)
			lines <- line
			continuousCount++
			// read continuously up to 100 lines
			if continuousCount < maxContinuouslyLines {
				continue
			}
			// if more - set timeout and wait ctx or times
			p.logger.Debugf("readFileWithTimeout: pause reading file after %d lines file for %d ms", maxContinuouslyLines, readContinuousLinesTimeout.Milliseconds())
			continuousCount = 0
			timer.Reset(readContinuousLinesTimeout)
		}

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			continue
		}
	}
}

// watchLogFile watches the log file for changes
func (p *Processor) watchLogFile(rotateChan chan bool) error {
	retryCount := 0
	for {
		err := p.watchLogFileWithRetry(rotateChan)

		if err == nil {
			return nil
		}

		retryCount++
		p.logger.Errorf("error watching file: %v", err)
		if retryCount >= maxRetries {
			return errors.New("max retries reached for log file watcher, giving up")
		}
		p.logger.Infof("retrying in %d ms (attempt %d/%d)",
			defaultRetryInterval.Milliseconds(), retryCount, maxRetries)

		select {
		case <-p.ctx.Done():
			return nil
		case <-time.After(defaultRetryInterval):
			continue
		}
	}
}

func (p *Processor) watchLogFileWithRetry(rotateChan chan bool) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %v", err)
	}
	defer func(watcher *fsnotify.Watcher) {
		_ = watcher.Close()
	}(watcher)

	// Watch the directory containing the log file
	logDir := filepath.Dir(p.cfg.DNSLog.Path)
	if err := watcher.Add(logDir); err != nil {
		return fmt.Errorf("failed to watch log directory: %v", err)
	}

	for {
		select {
		case <-p.ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("error reading event from watcher channel (it is closed)")
			}

			if event.Op&fsnotify.Create == fsnotify.Create &&
				event.Name == p.cfg.DNSLog.Path {
				p.logger.Info("Detected log file creation, rotating logs...")
				p.isRotated = true
				rotateChan <- true
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return errors.New("error reading error from watcher channel (it is closed)")
			}
			return fmt.Errorf("file watcher error: %v", err)
		}
	}
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

	// Get group for domain
	group, domainRule := p.getDomainGroup(domain)

	return &LogEntry{
		ClientIP:   clientIP,
		Domain:     domain,
		DomainRule: domainRule,
		QueryType:  queryType,
		Group:      group,
	}, nil
}

// getDomainGroup returns the group name for a monitored domain, or empty string if not monitored
func (p *Processor) getDomainGroup(domain string) (string, string) {
	// Check exact match first
	if group, ok := p.domains[strings.ToLower(domain)]; ok {
		return group, domain
	}

	// Check subdomains
	for d, group := range p.domains {
		if strings.HasSuffix("."+domain, "."+d) {
			return group, d
		}
	}

	return "", ""
}
