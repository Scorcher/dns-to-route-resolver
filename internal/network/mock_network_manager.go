package network

import (
	"net"
	"sync"
)

// MockNetworkManager is a mock implementation of the NetworkManager interface for testing
type MockNetworkManager struct {
	sync.Mutex
	routes map[string]struct{}
}

// NewMockNetworkManager creates a new MockNetworkManager
func NewMockNetworkManager() *MockNetworkManager {
	return &MockNetworkManager{
		routes: make(map[string]struct{}),
	}
}

// AddNetwork adds a network to the routing table
func (m *MockNetworkManager) AddNetwork(ip net.IP) error {
	m.Lock()
	defer m.Unlock()

	if m.routes == nil {
		m.routes = make(map[string]struct{})
	}

	m.routes[ip.String()] = struct{}{}
	return nil
}

// RemoveNetwork removes a network from the routing table
func (m *MockNetworkManager) RemoveNetwork(nw *net.IPNet) error {
	m.Lock()
	defer m.Unlock()

	if m.routes != nil {
		delete(m.routes, nw.String())
	}
	return nil
}

// GetKnownNetworks returns a list of known networks
func (m *MockNetworkManager) GetKnownNetworks() []net.IPNet {
	m.Lock()
	defer m.Unlock()

	var networks []net.IPNet
	for r := range m.routes {
		_, nw, err := net.ParseCIDR(r)
		if err != nil {
			continue
		}
		networks = append(networks, *nw)
	}
	return networks
}

// Stop cleans up resources
func (m *MockNetworkManager) Stop() {
	// No-op for the mock
}

// AddRoute is a helper method for testing to directly add a route
func (m *MockNetworkManager) AddRoute(route string) {
	m.Lock()
	defer m.Unlock()

	if m.routes == nil {
		m.routes = make(map[string]struct{})
	}

	m.routes[route] = struct{}{}
}
