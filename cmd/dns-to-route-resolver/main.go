package main

import (
	"context"
	"flag"
	"github.com/Scorcher/dns-to-route-resolver/internal/app"
	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	configPath = flag.String("config", "configs/config.yaml", "path to config file")
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

	// Handle shutdown signals with context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalCh
		logger.Debug("Received shutdown signal, cancelling context")
		cancel()
	}()

	// Create application
	application, err := app.NewApp(cfg)
	if err != nil {
		logger.Fatal("Failed to create application: " + err.Error())
	}

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Run the application
		if err := application.Run(ctx); err != nil {
			logger.Error("Application error: " + err.Error())
			cancel()
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	wg.Wait()
	logger.Info("Shutdown complete")
}
