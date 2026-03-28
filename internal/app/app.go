package app

import (
	"context"
	"fmt"
	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
	"github.com/Scorcher/dns-to-route-resolver/internal/logprocessor"
	"github.com/Scorcher/dns-to-route-resolver/internal/metrics"
	"github.com/Scorcher/dns-to-route-resolver/internal/network"
	"net"
	"sync"
)

// App represents the main application
type App struct {
	cfg            *config.Config
	logger         *log.Logger
	metrics        *metrics.Collector
	logProcessor   *logprocessor.Processor
	networkManager *network.NetworkManager
	ctx            context.Context
	cancel         context.CancelFunc
}

// NewApp creates a new application instance
func NewApp(cfg *config.Config) (*App, error) {
	logger := log.GetLogger()

	// Initialize metrics collector
	metricsCollector := metrics.NewCollector(cfg, nil)

	// Initialize network manager
	netMgr := network.NewManager(cfg, metricsCollector)

	// Initialize log processor
	logProc := logprocessor.NewProcessor(cfg, metricsCollector)

	return &App{
		cfg:            cfg,
		logger:         logger,
		metrics:        metricsCollector,
		logProcessor:   logProc,
		networkManager: netMgr,
	}, nil
}

// Run start the application
func (a *App) Run(ctx context.Context) error {
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.logger.Info("Starting DNS to Route Resolver")

	errChan := make(chan error, 1)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Run metrics server
		if err := a.metrics.Run(a.ctx); err != nil {
			a.logger.Errorf("failed to start metrics server: %v", err)
			errChan <- err
		}
	}()

	// Init network manager
	if err := a.networkManager.Init(); err != nil {
		a.logger.Errorf("failed to start network manager: %v", err)
		errChan <- err
	}
	a.metrics.SetRoutesTotal(a.networkManager.GetCount())

	if a.cfg.DNSLog.Enabled {
		a.metrics.SetDnsLogEnabled(1)

		wg.Add(1)
		go func() {
			defer wg.Done()
			// Run log processor
			if err := a.logProcessor.Run(a.ctx); err != nil {
				a.logger.Errorf("failed to start log processor: %v", err)
				errChan <- err
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			// Run processing logs
			a.receiveLogs()
		}()
	} else {
		a.metrics.SetDnsLogEnabled(0)
	}

	a.logger.Info("DNS to Route Resolver started successfully")

	select {
	case err := <-errChan:
		a.cancel()
		wg.Wait()
		return err
	case <-ctx.Done():
	}

	a.logger.Info("DNS to Route Resolver stopping")

	// cancel application context
	a.cancel()

	a.networkManager.Flush()

	wg.Wait()

	return nil
}

// receiveLogs processes DNS log entries
func (a *App) receiveLogs() {
	for {
		select {
		case entry, ok := <-a.logProcessor.Events():
			if !ok {
				a.logger.Info("Log processor channel closed, stopping log processing")
				return
			}

			a.logger.Debug("receiveLogs: got event")
			a.handleDNSEntry(entry)

		case <-a.ctx.Done():
			return
		}
	}
}

// handleDNSEntry handles a single DNS log entry
func (a *App) handleDNSEntry(entry logprocessor.LogEntry) {
	a.metrics.IncDNSQueries(entry.Domain)
	a.logger.Debugf("handleDNSEntry: entry - %s", entry.ClientIP.String())

	// Skip localhost and non-A records
	if entry.ClientIP.IsLoopback() || entry.QueryType != "A" {
		return
	}
	a.logger.Debug("handleDNSEntry: skip loopback and non IPv4")

	// Add the network to our routing table
	_, nw, err := net.ParseCIDR(fmt.Sprintf("%s/%d", entry.ClientIP, a.cfg.Settings.NetworkMask))
	if err != nil {
		a.logger.Error("Failed to parse network: " + err.Error())
		return
	}
	a.logger.Debugf("handleDNSEntry: successfully parsed network: %s", nw.String())

	// Add the network to our routing table
	added := a.networkManager.AddNetwork(nw.IP, entry.Group)
	if !added {
		a.logger.Debugf("handleDNSEntry: network %s already exists in group %s", nw.String(), entry.Group)
		return
	}
	a.logger.Debugf("handleDNSEntry: network %s added to group %s", nw.String(), entry.Group)

	a.metrics.IncRoutesAdded()
	a.metrics.SetRoutesTotal(a.networkManager.GetCount())

	routes := a.networkManager.GetGroupRoutes(entry.Group)
	a.logger.Debug("handleDNSEntry: got group routes")

	// Save routes for this group
	if err := a.networkManager.SaveGroupRoutes(entry.Group, routes); err != nil {
		a.logger.Errorf("failed to save routes for group %s: %v", entry.Group, err)
	}
	a.logger.Debug("handleDNSEntry: group routes saved")
}
