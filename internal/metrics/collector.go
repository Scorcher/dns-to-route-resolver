package metrics

import (
	"context"
	"errors"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"sync"
	"time"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector collects metrics for the application
type Collector struct {
	routesAdded      *prometheus.CounterVec
	routesRemoved    *prometheus.CounterVec
	routesTotal      prometheus.Gauge
	dnsLogEnabled    prometheus.Gauge
	dnsLogProcessing prometheus.Gauge
	dnsQueries       *prometheus.CounterVec
	dnsQueryErrors   *prometheus.CounterVec
	birdReloads      *prometheus.CounterVec
	birdReloadErrors *prometheus.CounterVec
	logger           *log.Logger
	server           *http.Server
	cfg              *config.Config
}

// NewCollector creates a new metrics collector with optional registry for testing isolation. If registry is nil, uses the default prometheus registry.
func NewCollector(cfg *config.Config, registry prometheus.Registerer) *Collector {
	if registry == nil {
		registry = prometheus.DefaultRegisterer
	}

	// Create metrics
	routesAdded := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dns_to_route_routes_added_total",
			Help: "Total number of routes added to the routing table",
		},
		[]string{},
	)

	routesRemoved := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dns_to_route_routes_removed_total",
			Help: "Total number of routes removed from the routing table",
		},
		[]string{},
	)

	routesTotal := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "dns_to_route_routes_total",
			Help: "Current number of routes in the routing table",
		},
	)
	routesTotal.Set(0)

	dnsLogEnabled := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "dns_to_route_log_enabled",
			Help: "DNS Log file processing enabled (0 - disabled, 1 - enabled)",
		},
	)
	dnsLogEnabled.Set(0)

	dnsLogProcessing := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "dns_to_route_log_processing_state",
			Help: "DNS Log file processing state (0 - not processing, 1 - processing)",
		},
	)
	dnsLogProcessing.Set(0)

	dnsQueries := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dns_to_route_dns_queries_total",
			Help: "Total number of DNS queries processed",
		},
		[]string{"domain"},
	)

	dnsQueryErrors := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dns_to_route_dns_query_errors_total",
			Help: "Total number of DNS query errors",
		},
		[]string{"error"},
	)

	birdReloads := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dns_to_route_bird_reloads_total",
			Help: "Total number of BIRD configuration reloads",
		},
		[]string{},
	)
	birdReloads.WithLabelValues().Add(0)

	birdReloadErrors := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dns_to_route_bird_reload_errors_total",
			Help: "Total number of BIRD configuration reload errors",
		},
		[]string{"error"},
	)

	// Register metrics
	registry.MustRegister(
		routesAdded,
		routesRemoved,
		routesTotal,
		dnsLogEnabled,
		dnsLogProcessing,
		dnsQueries,
		dnsQueryErrors,
		birdReloads,
		birdReloadErrors,
	)

	return &Collector{
		routesAdded:      routesAdded,
		routesRemoved:    routesRemoved,
		routesTotal:      routesTotal,
		dnsLogEnabled:    dnsLogEnabled,
		dnsLogProcessing: dnsLogProcessing,
		dnsQueries:       dnsQueries,
		dnsQueryErrors:   dnsQueryErrors,
		birdReloads:      birdReloads,
		birdReloadErrors: birdReloadErrors,
		logger:           log.GetLogger(),
		cfg:              cfg,
	}
}

// Run starts the metrics HTTP server
func (c *Collector) Run(ctx context.Context, cmdChan chan string) error {
	if !c.cfg.Metrics.Enabled {
		c.logger.Info("Metrics collection is disabled")
		return nil
	}

	// Create HTTP server
	mux := http.NewServeMux()
	mux.Handle(c.cfg.Metrics.Path, promhttp.Handler())

	// Add health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Add health check endpoint
	mux.HandleFunc("/cleanup", func(w http.ResponseWriter, r *http.Request) {
		cmdChan <- config.CommandNameCleanup
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("SCHEDULED"))
	})

	c.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", c.cfg.Metrics.Port),
		Handler: mux,
	}

	errChan := make(chan error, 1)

	// Start HTTP server in a goroutine
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()

		c.logger.Info(fmt.Sprintf("Starting metrics server on :%d%s", c.cfg.Metrics.Port, c.cfg.Metrics.Path))

		if err := c.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.logger.Error(fmt.Sprintf("Failed to start metrics server: %v", err))
			errChan <- err
		}
	}()

	select {
	case <-errChan:
	case <-ctx.Done():
	}

	if c.server != nil {
		ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.server.Shutdown(ctxTimeout)
	}

	wg.Wait()

	return nil
}

// IncRoutesAdded increments the routes added counter
func (c *Collector) IncRoutesAdded() {
	c.routesAdded.WithLabelValues().Inc()
}

// IncRoutesRemoved increments the routes removed counter
func (c *Collector) IncRoutesRemoved() {
	c.routesRemoved.WithLabelValues().Inc()
}

// SetRoutesTotal sets the total number of routes
func (c *Collector) SetRoutesTotal(count int) {
	c.routesTotal.Set(float64(count))
}

// SetDnsLogEnabled sets the dns log processing enabled state
func (c *Collector) SetDnsLogEnabled(state int) {
	c.dnsLogEnabled.Set(float64(state))
}

// SetDnsLogProcessing sets the dns log processing state
func (c *Collector) SetDnsLogProcessing(state int) {
	c.dnsLogProcessing.Set(float64(state))
}

// IncDNSQueries increments the DNS queries counter
func (c *Collector) IncDNSQueries(domain string) {
	c.dnsQueries.WithLabelValues(domain).Inc()
}

// IncDNSErrors increments the DNS errors counter
func (c *Collector) IncDNSErrors(err string) {
	c.dnsQueryErrors.WithLabelValues(err).Inc()
}

// IncBIRDReloads increments the BIRD reloads counter
func (c *Collector) IncBIRDReloads() {
	c.birdReloads.WithLabelValues().Inc()
}

// IncBIRDReloadErrors increments the BIRD reload errors counter
func (c *Collector) IncBIRDReloadErrors(err string) {
	c.birdReloadErrors.WithLabelValues(err).Inc()
}
