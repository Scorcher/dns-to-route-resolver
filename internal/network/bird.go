package network

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
)

// BirdManager handles BIRD configuration and control
type BirdManager struct {
	cfg                *config.Config
	logger             *log.Logger
	configPathTemplate string
	mu                 sync.Mutex
}

// NewBirdManager creates a new BirdManager instance
func NewBirdManager(cfg *config.Config) *BirdManager {
	return &BirdManager{
		cfg:                cfg,
		logger:             log.GetLogger(),
		configPathTemplate: cfg.Bird.ConfigPathTemplate,
	}
}

// Init initializes the BIRD manager
func (b *BirdManager) Init() error {
	// Ensure the config directory exists
	if err := os.MkdirAll(filepath.Dir(b.configPathTemplate), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	return nil
}

// AddRoute adds a network route to BIRD
func (b *BirdManager) AddRoute(nw *net.IPNet) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Read current config
	configData, err := os.ReadFile(b.configPathTemplate)
	if err != nil {
		return fmt.Errorf("failed to read BIRD config: %w", err)
	}

	// Check if route already exists
	routeStr := fmt.Sprintf(b.cfg.Bird.RouteTemplate, nw.String())
	if bytes.Contains(configData, []byte(routeStr)) {
		return nil // Already exists
	}

	// Add the new route
	var newConfig []byte
	if len(configData) > 0 && configData[len(configData)-1] != '\n' {
		newConfig = append(configData, '\n')
	} else {
		newConfig = make([]byte, len(configData))
		copy(newConfig, configData)
	}

	newConfig = append(newConfig, []byte(routeStr)...)

	// Write new config
	if err := os.WriteFile(b.configPathTemplate, newConfig, 0644); err != nil {
		return fmt.Errorf("failed to write BIRD config: %w", err)
	}

	// Reload BIRD configuration
	return b.reloadConfig()
}

// RemoveRoute removes a network route from BIRD
func (b *BirdManager) RemoveRoute(nw *net.IPNet) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Read current config
	configData, err := os.ReadFile(b.configPathTemplate)
	if err != nil {
		return fmt.Errorf("failed to read BIRD config: %w", err)
	}

	// Remove the route
	routeStr := fmt.Sprintf(b.cfg.Bird.RouteTemplate, nw.String())
	newConfig := bytes.ReplaceAll(configData, []byte(routeStr), []byte{})

	// Only write if something changed
	if !bytes.Equal(configData, newConfig) {
		// Remove empty lines
		newConfig = bytes.TrimSpace(newConfig)
		newConfig = append(newConfig, '\n')

		// Write new config
		if err := os.WriteFile(b.configPathTemplate, newConfig, 0644); err != nil {
			return fmt.Errorf("failed to write BIRD config: %w", err)
		}

		// Reload BIRD configuration
		return b.reloadConfig()
	}

	return nil
}

// reloadConfig reloads the BIRD configuration
func (b *BirdManager) reloadConfig() error {
	if len(b.cfg.Bird.ReloadCommand) == 0 {
		return nil
	}

	cmd := exec.Command(b.cfg.Bird.ReloadCommand[0], b.cfg.Bird.ReloadCommand[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reload BIRD config: %w\nOutput: %s", err, string(output))
	}

	b.logger.Info("BIRD configuration reloaded successfully")
	return nil
}

// GetRoutes returns the list of currently configured routes in BIRD
func (b *BirdManager) GetRoutes() ([]net.IPNet, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Read current config
	configData, err := os.ReadFile(b.configPathTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to read BIRD config: %w", err)
	}

	// Parse routes from config
	var routes []net.IPNet
	lines := strings.Split(string(configData), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "route ") && strings.HasSuffix(line, ";") {
			// Extract the network part (between "route " and " via")
			nwPart := strings.TrimPrefix(line, "route ")
			nwPart = strings.Split(nwPart, " ")[0]
			nwPart = strings.TrimSuffix(nwPart, ";")

			// Parse the network
			_, nw, err := net.ParseCIDR(nwPart)
			if err != nil {
				b.logger.Warn("Failed to parse route: " + err.Error())
				continue
			}

			routes = append(routes, *nw)
		}
	}

	return routes, nil
}
