package network

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
)

// NetworkManager manages network routes and interfaces
type NetworkManager struct {
	cfg            *config.Config
	logger         *log.Logger
	bird           *BirdManager
	knownNets      map[string]map[string]struct{}
	countKnownNets int
	mu             sync.RWMutex
}

// NewManager creates a new NetworkManager instance
func NewManager(cfg *config.Config) *NetworkManager {
	return &NetworkManager{
		cfg:            cfg,
		logger:         log.GetLogger(),
		knownNets:      make(map[string]map[string]struct{}),
		countKnownNets: 0,
		bird:           NewBirdManager(cfg),
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
		} else {
			for group := range m.knownNets {
				if err := m.SaveGroupRoutes(group, m.GetGroupRoutes(group)); err != nil {
					return err
				}
			}
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

// AddNetwork adds a network to the routing table for a specific group
func (m *NetworkManager) AddNetwork(ip net.IP, group string) error {
	// Convert IP to /24 network
	network := ipToNetwork(ip, m.cfg.Settings.NetworkMask)
	networkStr := network.String()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if we already know about this network in this group
	if groupMap, exists := m.knownNets[group]; exists {
		if _, netExists := groupMap[networkStr]; netExists {
			return nil // Already exists
		}
	}

	// Add to known networks
	if m.knownNets[group] == nil {
		m.knownNets[group] = make(map[string]struct{})
	}
	m.knownNets[group][networkStr] = struct{}{}
	m.countKnownNets++

	m.logger.Info("Added network: " + networkStr + " for group: " + group)

	return nil
}

// RemoveNetwork removes a network from the routing table
func (m *NetworkManager) RemoveNetwork(nw *net.IPNet) error {
	nwStr := nw.String()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Find which group contains this network
	var foundGroup string
	for group, groupMap := range m.knownNets {
		if _, exists := groupMap[nwStr]; exists {
			foundGroup = group
			break
		}
	}

	if foundGroup == "" {
		return nil // Not found, nothing to do
	}

	// Remove from known networks
	delete(m.knownNets[foundGroup], nwStr)
	m.countKnownNets--
	if len(m.knownNets[foundGroup]) == 0 {
		delete(m.knownNets, foundGroup)
	}

	m.logger.Info("Removed network: " + nwStr + " from group: " + foundGroup)

	return nil
}

// SaveGroupRoutes saves all routes for a group to BIRD
func (m *NetworkManager) SaveGroupRoutes(group string, routes []string) error {
	if err := m.bird.SaveGroupRoutes(group, routes); err != nil {
		return err
	}

	if err := m.bird.ReloadConfig(); err != nil {
		return err
	}

	return nil
}

// GetGroupRoutes returns all routes for a group
func (m *NetworkManager) GetGroupRoutes(group string) []string {
	m.mu.RLock()
	groupMap, exists := m.knownNets[group]
	m.mu.RUnlock()

	routes := make([]string, 0, len(groupMap))
	if exists {
		for networkStr := range groupMap {
			routes = append(routes, networkStr)
		}
	}

	return routes
}

// GetCount returns count of known networks
func (m *NetworkManager) GetCount() int {
	return m.countKnownNets
}

// loadKnownNetworks loads known networks from disk
func (m *NetworkManager) loadKnownNetworks() error {
	data, err := os.ReadFile(m.cfg.Persistence.StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist, no state to load
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	var state map[string][]string
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to parse state file: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	countKnownNets := 0
	for group, networks := range state {
		groupMap := make(map[string]struct{})
		countKnownNets = 0
		for _, nwStr := range networks {
			groupMap[nwStr] = struct{}{}
			countKnownNets++
		}
		m.knownNets[group] = groupMap
		m.countKnownNets += countKnownNets
	}

	m.logger.Info("Loaded known networks from state file")
	return nil
}

// saveKnownNetworks saves known networks to disk
func (m *NetworkManager) saveKnownNetworks() error {
	m.mu.RLock()
	state := make(map[string][]string)
	for group, groupMap := range m.knownNets {
		networks := make([]string, 0, len(groupMap))
		for nwStr := range groupMap {
			networks = append(networks, nwStr)
		}
		state[group] = networks
	}
	m.mu.RUnlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(m.cfg.Persistence.StateFile, data, 0640); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	m.logger.Info("Saved known networks to state file")
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
