package network

import (
	"fmt"
	"net"
	"sync"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
)

// NetworkManager manages network routes and interfaces
type NetworkManager struct {
	cfg       *config.Config
	logger    *log.Logger
	bird      *BirdManager
	knownNets map[string]struct{}
	mu        sync.RWMutex
}

// NewManager creates a new NetworkManager instance
func NewManager(cfg *config.Config) *NetworkManager {
	return &NetworkManager{
		cfg:       cfg,
		logger:    log.GetLogger(),
		knownNets: make(map[string]struct{}),
		bird:      NewBirdManager(cfg),
	}
}

// Start initializes the network manager
func (m *NetworkManager) Start() error {
	// Initialize BIRD manager
	if err := m.bird.Init(); err != nil {
		return fmt.Errorf("failed to initialize BIRD manager: %w", err)
	}

	// Load known networks from BIRD configuration if needed
	if m.cfg.Persistence.StateFile != "" {
		if err := m.loadKnownNetworks(); err != nil {
			m.logger.Warn("Failed to load known networks: " + err.Error())
		}
	}

	return nil
}

// Stop cleans up resources
func (m *NetworkManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clean up BIRD configuration if needed
	if m.cfg.Persistence.StateFile != "" {
		if err := m.saveKnownNetworks(); err != nil {
			m.logger.Error("Failed to save known networks: " + err.Error())
		}
	}
}

// AddNetwork adds a network to the routing table
func (m *NetworkManager) AddNetwork(ip net.IP) error {
	// Convert IP to /24 network
	nw := ipToNetwork(ip, m.cfg.Settings.NetworkMask)
	nwStr := nw.String()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if we already know about this network
	if _, exists := m.knownNets[nwStr]; exists {
		return nil // Already exists
	}

	// Add to BIRD configuration
	if err := m.bird.AddRoute(nw); err != nil {
		return fmt.Errorf("failed to add route to BIRD: %w", err)
	}

	// Add to known networks
	m.knownNets[nwStr] = struct{}{}
	m.logger.Info("Added network: " + nwStr)

	return nil
}

// RemoveNetwork removes a network from the routing table
func (m *NetworkManager) RemoveNetwork(nw *net.IPNet) error {
	nwStr := nw.String()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if we know about this network
	if _, exists := m.knownNets[nwStr]; !exists {
		return nil // Doesn't exist, nothing to do
	}

	// Remove from BIRD configuration
	if err := m.bird.RemoveRoute(nw); err != nil {
		return fmt.Errorf("failed to remove route from BIRD: %w", err)
	}

	// Remove from known networks
	delete(m.knownNets, nwStr)
	m.logger.Info("Removed network: " + nwStr)

	return nil
}

// GetKnownNetworks returns a copy of known networks
func (m *NetworkManager) GetKnownNetworks() []net.IPNet {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nets := make([]net.IPNet, 0, len(m.knownNets))
	for nwStr := range m.knownNets {
		_, nw, err := net.ParseCIDR(nwStr)
		if err != nil {
			m.logger.Error("Failed to parse known network: " + err.Error())
			continue
		}
		nets = append(nets, *nw)
	}

	return nets
}

// loadKnownNetworks loads known networks from disk
func (m *NetworkManager) loadKnownNetworks() error {
	// TODO: Implement loading from file
	return nil
}

// saveKnownNetworks saves known networks to disk
func (m *NetworkManager) saveKnownNetworks() error {
	// TODO: Implement saving to file
	return nil
}

// ipToNetwork converts an IP to a network with the specified mask length
func ipToNetwork(ip net.IP, maskLen int) *net.IPNet {
	mask := net.CIDRMask(maskLen, 32) // IPv4 only for now
	return &net.IPNet{
		IP:   ip.Mask(mask),
		Mask: mask,
	}
}
