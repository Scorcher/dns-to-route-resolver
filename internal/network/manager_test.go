package network

import (
	"github.com/Scorcher/dns-to-route-resolver/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	cfg := &config.Config{}
	metricsCollector := metrics.NewCollector(cfg, prometheus.NewRegistry())
	mgr := NewManager(cfg, metricsCollector)

	assert.NotNil(t, mgr)
	assert.NotNil(t, mgr.cfg)
	assert.NotNil(t, mgr.logger)
	assert.NotNil(t, mgr.knownNets)
	assert.Equal(t, 0, mgr.countKnownNets)
	assert.NotNil(t, mgr.bird)
}

func TestNetworkManager_AddNetwork(t *testing.T) {
	tests := []struct {
		name          string
		ip            net.IP
		group         string
		expectStatus  bool
		expectCount   int
		expectInGroup bool
	}{
		{
			name:          "add new network",
			ip:            net.ParseIP("192.168.1.100"),
			group:         "group1",
			expectStatus:  true,
			expectCount:   1,
			expectInGroup: true,
		},
		{
			name:          "add duplicate network",
			ip:            net.ParseIP("192.168.1.100"),
			group:         "group1",
			expectStatus:  false,
			expectCount:   1,
			expectInGroup: true,
		},
		{
			name:          "add network to different group",
			ip:            net.ParseIP("10.0.0.1"),
			group:         "group2",
			expectStatus:  true,
			expectCount:   2,
			expectInGroup: true,
		},
	}

	cfg := &config.Config{
		Settings: config.Settings{NetworkMask: 24},
	}
	metricsCollector := metrics.NewCollector(cfg, prometheus.NewRegistry())
	mgr := NewManager(cfg, metricsCollector)

	var status bool

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			status = mgr.AddNetwork(tt.ip, tt.group)
			if !tt.expectStatus {
				assert.False(t, status)
			} else {
				assert.True(t, status)
				assert.Equal(t, tt.expectCount, mgr.GetCount())

				if tt.expectInGroup {
					mgr.stateMutex.RLock()
					groupMap, exists := mgr.knownNets[tt.group]
					mgr.stateMutex.RUnlock()

					assert.True(t, exists)
					expectedNetwork := ipToNetwork(tt.ip, 24).String()
					_, networkExists := groupMap[expectedNetwork]
					assert.True(t, networkExists)
				}
			}
		})
	}
}

func TestNetworkManager_RemoveNetwork(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{NetworkMask: 24},
	}
	metricsCollector := metrics.NewCollector(cfg, prometheus.NewRegistry())
	mgr := NewManager(cfg, metricsCollector)

	var status bool

	// Add a network first
	ip := net.ParseIP("192.168.1.100")
	group := "test-group"
	status = mgr.AddNetwork(ip, group)
	assert.True(t, status)
	assert.Equal(t, 1, mgr.GetCount())

	// Remove the network
	network := ipToNetwork(ip, 24)
	status = mgr.RemoveNetwork(network)
	assert.True(t, status)
	assert.Equal(t, 0, mgr.GetCount())

	// Verify it's removed
	mgr.stateMutex.RLock()
	_, exists := mgr.knownNets[group]
	mgr.stateMutex.RUnlock()
	assert.False(t, exists)

	// Try to remove non-existing network (should not error)
	status = mgr.RemoveNetwork(network)
	assert.False(t, status)
}

func TestNetworkManager_GetCount(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{NetworkMask: 24},
	}
	metricsCollector := metrics.NewCollector(cfg, prometheus.NewRegistry())
	mgr := NewManager(cfg, metricsCollector)

	assert.Equal(t, 0, mgr.GetCount())

	// Add networks
	_ = mgr.AddNetwork(net.ParseIP("192.168.1.1"), "group1")
	_ = mgr.AddNetwork(net.ParseIP("192.168.2.1"), "group1")
	_ = mgr.AddNetwork(net.ParseIP("10.0.0.1"), "group2")

	assert.Equal(t, 3, mgr.GetCount())
}

func TestNetworkManager_ConcurrentAccess(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{NetworkMask: 24},
	}
	metricsCollector := metrics.NewCollector(cfg, prometheus.NewRegistry())
	mgr := NewManager(cfg, metricsCollector)

	var wg sync.WaitGroup
	numGoroutines := 10
	operationsPerGoroutine := 100

	// Start multiple goroutines adding and removing networks
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				ip := net.IPv4(byte(id), byte(j%256), byte(j/256), 1)
				_ = mgr.AddNetwork(ip, "group1")
			}
		}(i)
	}

	wg.Wait()

	// Count should be reasonable (some duplicates expected)
	count := mgr.GetCount()
	assert.True(t, count > 0)
	assert.True(t, count <= numGoroutines*operationsPerGoroutine)
}

func TestNetworkManager_Persistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dns-to-route-test-*")
	require.NoError(t, err)
	defer func(path string) {
		_ = os.RemoveAll(path)
	}(tempDir)

	stateFile := filepath.Join(tempDir, "state.json")

	cfg := &config.Config{
		Settings:    config.Settings{NetworkMask: 24},
		Persistence: config.PersistenceConfig{StateFile: stateFile},
	}

	metricsCollector := metrics.NewCollector(cfg, prometheus.NewRegistry())
	// Create manager and add some networks
	mgr1 := NewManager(cfg, metricsCollector)

	_ = mgr1.AddNetwork(net.ParseIP("192.168.1.1"), "group1")
	_ = mgr1.AddNetwork(net.ParseIP("192.168.2.1"), "group1")
	_ = mgr1.AddNetwork(net.ParseIP("10.0.0.1"), "group2")

	// Save state
	err = mgr1.saveKnownNetworks()
	require.NoError(t, err)

	// Create new manager and load state
	mgr2 := NewManager(cfg, metricsCollector)

	err = mgr2.loadKnownNetworks()
	require.NoError(t, err)

	assert.Equal(t, mgr1.GetCount(), mgr2.GetCount())

	// Verify loaded networks
	mgr2.stateMutex.RLock()
	group1Map, exists1 := mgr2.knownNets["group1"]
	group2Map, exists2 := mgr2.knownNets["group2"]
	mgr2.stateMutex.RUnlock()

	assert.True(t, exists1)
	assert.True(t, exists2)
	assert.Len(t, group1Map, 2)
	assert.Len(t, group2Map, 1)

	_, hasNet1 := group1Map["192.168.1.0/24"]
	_, hasNet2 := group1Map["192.168.2.0/24"]
	_, hasNet3 := group2Map["10.0.0.0/24"]

	assert.True(t, hasNet1)
	assert.True(t, hasNet2)
	assert.True(t, hasNet3)
}

func TestIpToNetwork(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		maskLen  int
		expected string
	}{
		{
			name:     "IPv4 /24",
			ip:       net.ParseIP("192.168.1.100"),
			maskLen:  24,
			expected: "192.168.1.0/24",
		},
		{
			name:     "IPv4 /16",
			ip:       net.ParseIP("192.168.1.100"),
			maskLen:  16,
			expected: "192.168.0.0/16",
		},
		{
			name:     "IPv4 /32",
			ip:       net.ParseIP("192.168.1.100"),
			maskLen:  32,
			expected: "192.168.1.100/32",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ipToNetwork(tt.ip, tt.maskLen)
			assert.Equal(t, tt.expected, result.String())
		})
	}
}
