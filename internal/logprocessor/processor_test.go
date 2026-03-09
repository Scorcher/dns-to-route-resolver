package logprocessor

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProcessor(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			MonitoredDomains: []string{"example.com", "test.org"},
		},
	}
	metricsCollector := metrics.NewCollector(cfg, prometheus.NewRegistry())
	p := NewProcessor(cfg, metricsCollector)

	assert.NotNil(t, p)
	assert.NotNil(t, p.eventChan)
	assert.NotNil(t, p.done)
	assert.Len(t, p.domains, 2)
	assert.Contains(t, p.domains, "example.com")
	assert.Contains(t, p.domains, "test.org")
}

func TestProcessor_ParseLogLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    *LogEntry
		wantErr bool
	}{
		{
			name: "valid A record",
			line: "[2023-01-01 12:34:56] 192.168.1.100 example.com A",
			want: &LogEntry{
				Domain:    "example.com",
				ClientIP:  net.ParseIP("192.168.1.100"),
				QueryType: "A",
			},
			wantErr: false,
		},
		{
			name: "valid AAAA record",
			line: "[2023-01-01 12:34:56] 2001:db8::1 example.com AAAA",
			want: &LogEntry{
				Domain:    "example.com",
				ClientIP:  net.ParseIP("2001:db8::1"),
				QueryType: "AAAA",
			},
			wantErr: false,
		},
		{
			name:    "invalid line format",
			line:    "invalid log line",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid IP",
			line:    "[2023-01-01 12:34:56] 999.999.999.999 example.com A",
			want:    nil,
			wantErr: true,
		},
	}

	cfg := &config.Config{
		Network: config.NetworkConfig{
			MonitoredDomains: []string{"example.com"},
		},
	}

	p := &Processor{
		cfg:     cfg,
		metrics: metrics.NewCollector(cfg, prometheus.NewRegistry()),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.parseLogLine(tt.line)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.want.Domain, got.Domain)
			assert.Equal(t, tt.want.ClientIP, got.ClientIP)
			assert.Equal(t, tt.want.QueryType, got.QueryType)
		})
	}
}

func TestProcessor_IsMonitoredDomain(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		monitor  []string
		expected bool
	}{
		{
			name:     "exact match",
			domain:   "example.com",
			monitor:  []string{"example.com"},
			expected: true,
		},
		{
			name:     "subdomain match",
			domain:   "sub.example.com",
			monitor:  []string{"example.com"},
			expected: true,
		},
		{
			name:     "no match",
			domain:   "other.com",
			monitor:  []string{"example.com"},
			expected: false,
		},
		{
			name:     "empty monitor list",
			domain:   "example.com",
			monitor:  []string{},
			expected: false,
		},
		{
			name:     "case insensitive match",
			domain:   "Example.COM",
			monitor:  []string{"example.com"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Network: config.NetworkConfig{
					MonitoredDomains: tt.monitor,
				},
			}

			p := &Processor{
				cfg:       cfg,
				domains:   make(map[string]struct{}),
				metrics:   metrics.NewCollector(cfg, prometheus.NewRegistry()),
				eventChan: make(chan LogEntry, 10),
				done:      make(chan struct{}),
			}

			// Initialize domains map
			for _, d := range tt.monitor {
				p.domains[strings.ToLower(d)] = struct{}{}
			}

			assert.Equal(t, tt.expected, p.isMonitoredDomain(tt.domain))
		})
	}
}

func TestProcessor_ProcessLogFile(t *testing.T) {
	// Create a temporary log file
	tempDir, err := os.MkdirTemp("", "dns-to-route-test-*")
	require.NoError(t, err)
	defer func(path string) {
		_ = os.RemoveAll(path)
	}(tempDir)

	logFile := filepath.Join(tempDir, "query.log")
	f, err := os.Create(logFile)
	require.NoError(t, err)

	t.Log(logFile)

	// Write test entries
	_, err = f.WriteString("[2023-01-01 12:34:56] 192.168.1.100 example.com A\n")
	require.NoError(t, err)
	_, err = f.WriteString("[2023-01-01 12:35:00] 10.0.0.1 example.org A\n")
	require.NoError(t, err)
	_ = f.Close()

	// Create config
	cfg := &config.Config{
		DNSLog: config.DNSLogConfig{
			Path:   logFile,
			Follow: false,
		},
		Network: config.NetworkConfig{
			MonitoredDomains: []string{"example.com"},
		},
	}

	// Create processor with metrics
	p := NewProcessor(cfg, metrics.NewCollector(cfg, prometheus.NewRegistry()))

	// Start processing in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.StartInternal()
	}()

	// Wait for the processor to start and process the file
	time.Sleep(500 * time.Millisecond)

	// Should receive one event for example.com
	select {
	case entry := <-p.Events():
		assert.Equal(t, "example.com", entry.Domain)
		assert.Equal(t, "192.168.1.100", entry.ClientIP.String())
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for log event")
	}

	// No more events should be available
	select {
	case entry := <-p.Events():
		t.Fatalf("Unexpected log entry: %+v", entry)
	default:
		// Expected
	}

	// Stop the processor
	p.StopInternal()
	wg.Wait()
}

func TestProcessor_StartStop(t *testing.T) {
	// Create config
	cfg := &config.Config{
		DNSLog: config.DNSLogConfig{
			Path: "",
		},
	}

	// Test with no log file (should not fail)
	p := NewProcessor(cfg, metrics.NewCollector(cfg, prometheus.NewRegistry()))

	err := p.Start()
	assert.NoError(t, err)
	p.Stop()
}

func TestProcessor_FileNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dns-to-route-test-*")
	require.NoError(t, err)
	defer func(path string) {
		_ = os.RemoveAll(path)
	}(tempDir)

	nonexistentFile := filepath.Join(tempDir, "nonexistent.log")

	// Create config
	cfg := &config.Config{
		DNSLog: config.DNSLogConfig{
			Path: nonexistentFile,
		},
	}

	// Test with non-existent log file (should not fail on Start, but log a warning)
	p := NewProcessor(cfg, metrics.NewCollector(cfg, prometheus.NewRegistry()))

	err = p.Start()
	assert.NoError(t, err)
	p.Stop()
}
