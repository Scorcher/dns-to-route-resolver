package network

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkManager(t *testing.T) {
	// Create a temporary directory for test data
	tempDir, err := os.MkdirTemp("", "dns-to-route-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a test config
	testConfig := &config.Config{
		Persistence: config.PersistenceConfig{
			StateFile: filepath.Join(tempDir, "state.json"),
		},
		Settings: config.Settings{
			NetworkMask: 24,
		},
	}

	// Initialize the network manager
	mgr := NewManager(testConfig)

	t.Run("AddNetwork", func(t *testing.T) {
		ip := net.ParseIP("192.168.1.1")
		err := mgr.AddNetwork(ip)
		assert.NoError(t, err)

		// Verify the network was added
		nets := mgr.GetKnownNetworks()
		assert.Len(t, nets, 1)
		assert.Equal(t, "192.168.1.0/24", nets[0].String())
	})

	t.Run("AddDuplicateNetwork", func(t *testing.T) {
		ip := net.ParseIP("192.168.1.2") // Same /24 network as above
		err := mgr.AddNetwork(ip)
		assert.NoError(t, err)

		// Should still only have one network
		nets := mgr.GetKnownNetworks()
		assert.Len(t, nets, 1)
	})

	t.Run("AddDifferentNetwork", func(t *testing.T) {
		ip := net.ParseIP("10.0.0.1")
		err := mgr.AddNetwork(ip)
		assert.NoError(t, err)

		// Should now have two networks
		nets := mgr.GetKnownNetworks()
		assert.Len(t, nets, 2)
	})

	t.Run("RemoveNetwork", func(t *testing.T) {
		_, nw, _ := net.ParseCIDR("10.0.0.0/24")
		err := mgr.RemoveNetwork(nw)
		assert.NoError(t, err)

		// Should have one network left
		nets := mgr.GetKnownNetworks()
		assert.Len(t, nets, 1)
	})
}
