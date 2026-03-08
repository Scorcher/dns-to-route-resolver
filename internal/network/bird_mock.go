package network

import (
	"net"
	"sync"
)

// MockBirdManager is a mock implementation of the BirdManager interface for testing
type MockBirdManager struct {
	mu     sync.Mutex
	routes map[string]struct{}
}

// NewMockBirdManager creates a new MockBirdManager
func NewMockBirdManager() *MockBirdManager {
	return &MockBirdManager{
		routes: make(map[string]struct{}),
	}
}

// AddRoute adds a route to the mock BIRD configuration
func (m *MockBirdManager) AddRoute(nw *net.IPNet) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.routes[nw.String()] = struct{}{}
	return nil
}

// RemoveRoute removes a route from the mock BIRD configuration
func (m *MockBirdManager) RemoveRoute(nw *net.IPNet) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.routes, nw.String())
	return nil
}

// GetRoutes returns the list of routes in the mock BIRD configuration
func (m *MockBirdManager) GetRoutes() ([]net.IPNet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []net.IPNet
	for route := range m.routes {
		_, nw, err := net.ParseCIDR(route)
		if err != nil {
			return nil, err
		}
		result = append(result, *nw)
	}

	return result, nil
}
