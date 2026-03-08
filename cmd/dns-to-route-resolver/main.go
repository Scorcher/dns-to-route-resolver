package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Scorcher/dns-to-route-resolver/internal/app"
	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
)

var (
	configPath = flag.String("config", "/etc/dns-to-route-resolver/config.yaml", "path to config file")
	version    = "dev"
)

func main() {
	flag.Parse()

	// Initialize logger
	logger := log.NewLogger()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Fatal("Failed to load configuration: " + err.Error())
	}

	// Configure logger
	logger.SetLevel(cfg.GetLogLevel())
	log.SetGlobalLogger(logger)

	logger.Info("Starting DNS to Route Resolver (version: " + version + ")")
	logger.Info("Using configuration from: " + *configPath)

	// Handle shutdown signals
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM)

	// Create application
	application, err := app.NewApp(cfg, shutdownCh)
	if err != nil {
		logger.Fatal("Failed to create application: " + err.Error())
	}

	// Start the application in a goroutine
	if err := application.Start(); err != nil {
		logger.Error("Application error: " + err.Error())
		shutdownCh <- syscall.SIGTERM
	}

	// Wait for shutdown signal
	sig := <-shutdownCh
	close(shutdownCh)
	logger.Info("Received signal: " + sig.String())
	logger.Info("Shutting down...")

	// Stop the application
	application.Stop()

	// Wait for graceful shutdown
	application.WaitForShutdown(30 * time.Second)
	logger.Info("Shutdown complete")
}
