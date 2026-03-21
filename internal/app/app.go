package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
	"github.com/Scorcher/dns-to-route-resolver/internal/logprocessor"
	"github.com/Scorcher/dns-to-route-resolver/internal/metrics"
	"github.com/Scorcher/dns-to-route-resolver/internal/network"
)

// App represents the main application
type App struct {
	cfg            *config.Config
	logger         *log.Logger
	metrics        *metrics.Collector
	logProcessor   *logprocessor.Processor
	networkManager *network.NetworkManager
	shutdownCh     chan os.Signal
}

// NewApp creates a new application instance
func NewApp(cfg *config.Config, shutdownCh chan os.Signal) (*App, error) {
	logger := log.GetLogger()

	// Initialize metrics collector
	metricsCollector := metrics.NewCollector(cfg, nil)

	// Initialize network manager
	netMgr := network.NewManager(cfg)

	// Initialize log processor
	logProc := logprocessor.NewProcessor(cfg, metricsCollector)

	return &App{
		cfg:            cfg,
		logger:         logger,
		metrics:        metricsCollector,
		logProcessor:   logProc,
		networkManager: netMgr,
		shutdownCh:     shutdownCh,
	}, nil
}

// Start starts the application
func (a *App) Start() error {
	a.logger.Info("Starting DNS to Route Resolver")

	// Start metrics server
	if err := a.metrics.Start(); err != nil {
		return fmt.Errorf("failed to start metrics: %w", err)
	}

	// Start network manager
	if err := a.networkManager.Start(); err != nil {
		return fmt.Errorf("failed to start network manager: %w", err)
	}

	if a.cfg.DNSLog.Enabled {
		a.metrics.SetDnsLogEnabled(1)

		// Start log processor
		if err := a.logProcessor.Start(); err != nil {
			return fmt.Errorf("failed to start log processor: %w", err)
		}

		// Start processing logs
		go a.processLogs()
	} else {
		a.metrics.SetDnsLogEnabled(0)
	}

	a.logger.Info("DNS to Route Resolver started successfully")
	return nil
}

// Stop stops the application
func (a *App) Stop() {
	a.logger.Info("Shutting down DNS to Route Resolver...")

	if a.cfg.DNSLog.Enabled {
		// Stop log processor
		a.logProcessor.Stop()
	}

	// Stop network manager
	a.networkManager.Stop()

	// Stop metrics server
	a.metrics.Stop()
	a.logger.Info("DNS to Route Resolver stopped")
}

// processLogs processes DNS log entries
func (a *App) processLogs() {
	for {
		select {
		case entry, ok := <-a.logProcessor.Events():
			if !ok {
				a.logger.Info("Log processor channel closed, stopping log processing")
				return
			}

			a.handleDNSEntry(entry)

		case <-a.shutdownCh:
			return
		}
	}
}

// handleDNSEntry handles a single DNS log entry
func (a *App) handleDNSEntry(entry logprocessor.LogEntry) {
	a.metrics.IncDNSQueries(entry.Domain)

	// Skip localhost and non-A records
	if entry.ClientIP.IsLoopback() || entry.QueryType != "A" {
		return
	}

	// Add the network to our routing table
	_, nw, err := net.ParseCIDR(fmt.Sprintf("%s/%d", entry.ClientIP, a.cfg.Settings.NetworkMask))
	if err != nil {
		a.logger.Error("Failed to parse network: " + err.Error())
		return
	}

	// Add the network to our routing table
	if err := a.networkManager.AddNetwork(nw.IP, entry.Group); err != nil {
		a.logger.Error("Failed to add network: " + err.Error())
		a.metrics.IncDNSErrors(err)
		return
	}

	a.metrics.IncRoutesAdded()
	a.metrics.SetRoutesTotal(a.networkManager.GetCount())

	routes := a.networkManager.GetGroupRoutes(entry.Group)

	// Save routes for this group
	if err := a.networkManager.SaveGroupRoutes(entry.Group, routes); err != nil {
		a.logger.Errorf("failed to save routes for group %s: %v", entry.Group, err)
	}
}

// WaitForShutdown waits for the application to be shut down
func (a *App) WaitForShutdown(timeout time.Duration) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Wait for shutdown or timeout
	select {
	case <-a.shutdownCh:
		a.logger.Info("Shutdown complete")
	case <-ctx.Done():
		a.logger.Warn("Shutdown timed out, forcing exit")
	}
}
