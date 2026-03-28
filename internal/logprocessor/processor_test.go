package logprocessor

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
			MonitoredDomains: []config.DomainGroup{
				{Name: "test", Domains: []string{"example.com", "test.org"}},
			},
		},
	}
	metricsCollector := metrics.NewCollector(cfg, prometheus.NewRegistry())
	p := NewProcessor(cfg, metricsCollector)

	assert.NotNil(t, p)
	assert.NotNil(t, p.eventChan)
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
				Group:     "test",
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
				Group:     "test",
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
			MonitoredDomains: []config.DomainGroup{
				{Name: "test", Domains: []string{"example.com"}},
			},
		},
	}

	p := NewProcessor(cfg, metrics.NewCollector(cfg, prometheus.NewRegistry()))

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
			assert.Equal(t, tt.want.Group, got.Group)
		})
	}
}

func TestProcessor_GetDomainGroup(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		monitor  []config.DomainGroup
		expected string
	}{
		{
			name:     "exact match",
			domain:   "example.com",
			monitor:  []config.DomainGroup{{Name: "test", Domains: []string{"example.com"}}},
			expected: "test",
		},
		{
			name:     "subdomain match",
			domain:   "sub.example.com",
			monitor:  []config.DomainGroup{{Name: "test", Domains: []string{"example.com"}}},
			expected: "test",
		},
		{
			name:     "no match",
			domain:   "other.com",
			monitor:  []config.DomainGroup{{Name: "test", Domains: []string{"example.com"}}},
			expected: "",
		},
		{
			name:     "empty monitor list",
			domain:   "example.com",
			monitor:  []config.DomainGroup{},
			expected: "",
		},
		{
			name:     "case insensitive match",
			domain:   "Example.COM",
			monitor:  []config.DomainGroup{{Name: "test", Domains: []string{"example.com"}}},
			expected: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Network: config.NetworkConfig{
					MonitoredDomains: tt.monitor,
				},
			}

			p := NewProcessor(cfg, metrics.NewCollector(cfg, prometheus.NewRegistry()))

			assert.Equal(t, tt.expected, p.getDomainGroup(tt.domain))
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
			MonitoredDomains: []config.DomainGroup{
				{Name: "test", Domains: []string{"example.com"}},
			},
		},
	}

	// Create processor with metrics
	p := NewProcessor(cfg, metrics.NewCollector(cfg, prometheus.NewRegistry()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Start processing in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = p.Run(ctx)
	}()

	// Wait for the processor to start and process the file
	time.Sleep(500 * time.Millisecond)

	// Should receive one event for example.com
	select {
	case entry := <-p.Events():
		assert.Equal(t, "example.com", entry.Domain)
		assert.Equal(t, "192.168.1.100", entry.ClientIP.String())
		assert.Equal(t, "test", entry.Group)
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
	cancel()
	wg.Wait()
}

func TestProcessor_StartStop(t *testing.T) {

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

	_ = f.Close()

	// Create config
	cfg := &config.Config{
		DNSLog: config.DNSLogConfig{
			Path: logFile,
		},
	}

	// Test with no log file (should not fail)
	p := NewProcessor(cfg, metrics.NewCollector(cfg, prometheus.NewRegistry()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := p.Run(ctx)
		assert.NoError(t, err)
	}()
	cancel()

	// Wait for the goroutine to complete with a timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Goroutine completed successfully
	case <-time.After(2 * time.Second):
		t.Error("goroutine did not complete within 2 seconds")
	}
}

func TestProcessor_FileNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dns-to-route-test-*")
	require.NoError(t, err)
	defer func(path string) {
		_ = os.RemoveAll(path)
	}(tempDir)

	filename := "nonexistent.log"
	nonexistentFile := filepath.Join(tempDir, filename)

	// Create config
	cfg := &config.Config{
		DNSLog: config.DNSLogConfig{
			Path: nonexistentFile,
		},
	}

	// Test with non-existent log file (should not fail on Start, but log a warning)
	p := NewProcessor(cfg, metrics.NewCollector(cfg, prometheus.NewRegistry()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := p.Run(ctx)
		assert.Error(t, err)
		assert.Equal(t, fmt.Sprintf("log file does not exist: %s", filename), err.Error())
	}()
}
