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
	cmdChan := make(chan string, 1)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Run metrics server
		if err := a.metrics.Run(a.ctx, cmdChan); err != nil {
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		a.receiveCommand(cmdChan)
	}()

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

	a.networkManager.StoreNetworks()

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

// receiveCommand processes commands
func (a *App) receiveCommand(cmdChan <-chan string) {
	for {
		select {
		case cmd := <-cmdChan:
			a.logger.Debugf("receiveCommand: got command - %s", cmd)
			switch cmd {
			case config.CommandNameCleanup:
				a.networkManager.CleanupNetworks()
			default:
				a.logger.Errorf("receiveCommand: unknown command - %s", cmd)
			}
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

	listIp, err := resolveToIPv4(entry.Domain)
	if err != nil {
		a.logger.Warnf("handleDNSEntry: failed to resolve domain \"%s\" to IPv4: %v", entry.Domain, err)
		return
	}
	if len(listIp) == 0 {
		a.logger.Warnf("handleDNSEntry: IPv4 addresses not found in domain \"%s\"", entry.Domain)
		return
	}

	var nw string
	countAdded := 0
	for _, ip := range listIp {
		nw = getNetworkByMask(ip, a.cfg.Settings.NetworkMask)
		a.logger.Debugf("handleDNSEntry: successfully got network: %s", nw)

		// Add the network to our routing table
		added := a.networkManager.AddNetwork(nw, entry.Group)
		if !added {
			a.logger.Debugf("handleDNSEntry: network %s already exists in group %s", nw, entry.Group)
			continue
		}
		countAdded++
		a.logger.Debugf("handleDNSEntry: network %s added to group %s", nw, entry.Group)

		a.metrics.IncRoutesAdded()
		a.metrics.SetRoutesTotal(a.networkManager.GetCount())
	}

	if countAdded == 0 {
		// nothing to do anymore if nothing added
		return
	}

	routes := a.networkManager.GetGroupRoutes(entry.Group)
	a.logger.Debug("handleDNSEntry: got group routes")

	// Save routes for this group
	if err := a.networkManager.SaveGroupRoutes(entry.Group, routes); err != nil {
		a.logger.Errorf("failed to save routes for group %s: %v", entry.Group, err)
	}
	a.logger.Debug("handleDNSEntry: group routes saved")
}

func resolveToIPv4(hostname string) ([]net.IP, error) {
	var list []net.IP
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return list, err
	}

	for _, ip := range ips {
		// To4() возвращает nil для IPv6
		ipv4 := ip.To4()
		if ipv4 == nil {
			continue
		}
		// Опционально: исключаем специальные адреса
		if ipv4.IsLoopback() || ipv4.IsMulticast() || ipv4.IsLinkLocalUnicast() {
			continue
		}
		list = append(list, ipv4)
	}

	return list, nil
}

func getNetworkByMask(ip net.IP, maskBits int) string {
	// Определяем длину IP (32 для IPv4, 128 для IPv6)
	ipLen := 32
	if ip.To4() == nil {
		ipLen = 128
	}

	// Создаём маску
	mask := net.CIDRMask(maskBits, ipLen)

	// Применяем маску к IP
	networkIP := ip.Mask(mask)

	return fmt.Sprintf("%s/%d", networkIP, maskBits)
}
