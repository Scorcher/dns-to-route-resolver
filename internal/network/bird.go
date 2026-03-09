package network

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

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
	return nil
}

// SaveGroupRoutes saves all routes for a group to BIRD config file
func (b *BirdManager) SaveGroupRoutes(group string, routes []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	configPath := fmt.Sprintf(b.configPathTemplate, group)

	// Get existing file info if it exists
	var mode os.FileMode = 0644
	uid, gid := -1, -1
	if info, err := os.Stat(configPath); err == nil {
		mode = info.Mode()
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			uid = int(stat.Uid)
			gid = int(stat.Gid)
		}
	}

	// Create temp file in the same directory
	tempDir := filepath.Dir(configPath)
	tempFile, err := os.CreateTemp(tempDir, "bird-route-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		if tempFile != nil {
			_ = tempFile.Close()
			_ = os.Remove(tempPath)
		}
	}()

	// Write routes directly to temp file
	for _, route := range routes {
		if _, err := fmt.Fprintf(tempFile, b.cfg.Bird.RouteTemplate, route); err != nil {
			return fmt.Errorf("failed to write route to temp file: %w", err)
		}
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	tempFile = nil

	// Set permissions and ownership
	if err := os.Chmod(tempPath, mode); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}
	if uid != -1 || gid != -1 {
		if err := os.Chown(tempPath, uid, gid); err != nil {
			return fmt.Errorf("failed to set ownership: %w", err)
		}
	}

	// Atomic rename
	if err := os.Rename(tempPath, configPath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// ReloadConfig reloads the BIRD configuration
func (b *BirdManager) ReloadConfig() error {
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
