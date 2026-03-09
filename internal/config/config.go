package config

import (
	"fmt"
	"os"

	yaml "gopkg.in/yaml.v3"
	"path/filepath"

	"github.com/rs/zerolog"
)

// Config represents the application configuration
type Config struct {
	Log         LogConfig         `yaml:"log"`
	DNSLog      DNSLogConfig      `yaml:"dns_log"`
	Network     NetworkConfig     `yaml:"network"`
	Bird        BirdConfig        `yaml:"bird"`
	Metrics     MetricsConfig     `yaml:"metrics"`
	Persistence PersistenceConfig `yaml:"persistence"`
	Settings    Settings          `yaml:"settings"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	File   string `yaml:"file"`
}

type DNSLogConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
	Follow  bool   `yaml:"follow"`
}

type NetworkConfig struct {
	MonitoredDomains []string `yaml:"monitored_domains"`
}

type BirdConfig struct {
	ConfigPathTemplate string   `yaml:"config_path_template"`
	ReloadCommand      []string `yaml:"reload_command"`
	RouteTemplate      string   `yaml:"route_template"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Port    int    `yaml:"port"`
	Path    string `yaml:"path"`
}

type PersistenceConfig struct {
	StateFile    string `yaml:"state_file"`
	SaveInterval int    `yaml:"save_interval"`
}

type Settings struct {
	NetworkMask  int `yaml:"network_mask"`
	MaxRetries   int `yaml:"max_retries"`
	RetryBackoff int `yaml:"retry_backoff"`
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Log: LogConfig{
			Level:  "info",
			Format: "json",
			File:   "",
		},
		DNSLog: DNSLogConfig{
			Enabled: true,
			Path:    "/var/log/dnscrypt-proxy/query.log",
			Follow:  true,
		},
		Network: NetworkConfig{
			MonitoredDomains: []string{},
		},
		Bird: BirdConfig{
			ConfigPathTemplate: "/etc/bird/lst/dns-to-route-resolver.lst",
			ReloadCommand:      []string{"birdc", "configure"},
			RouteTemplate:      "route %s via 127.0.0.1;\n",
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Port:    9091,
			Path:    "/metrics",
		},
		Persistence: PersistenceConfig{
			StateFile:    "/var/lib/dns-to-route-resolver/state.json",
			SaveInterval: 300,
		},
		Settings: Settings{
			NetworkMask:  24,
			MaxRetries:   3,
			RetryBackoff: 5,
		},
	}
}

// Load loads configuration from a file
func Load(path string) (*Config, error) {
	// Use default config as base
	cfg := DefaultConfig()

	// Read config file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Unmarshal YAML into config struct
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}

// Save saves the configuration to a file
func (c *Config) Save(path string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal config to YAML
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// GetLogLevel returns the zerolog level from the config
func (c *Config) GetLogLevel() zerolog.Level {
	switch c.Log.Level {
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}
