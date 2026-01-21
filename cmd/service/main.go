// Binance Master Withdrawal Service
//
// A microservice that automatically forwards funds from a Binance master account
// to configured external wallets. It enables sub-accounts to effectively perform
// external withdrawals through an internal transfer → auto-forward pattern.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/binance-withdrawal/internal/api"
	"github.com/binance-withdrawal/internal/audit"
	"github.com/binance-withdrawal/internal/binance"
	"github.com/binance-withdrawal/internal/config"
	"github.com/binance-withdrawal/internal/detector"
	"github.com/binance-withdrawal/internal/forwarder"
	"github.com/binance-withdrawal/internal/model"
	"github.com/binance-withdrawal/internal/poller"
	"github.com/binance-withdrawal/internal/store"
	"gopkg.in/yaml.v3"
)

// Config represents the service configuration file structure.
type Config struct {
	Binance struct {
		APIKey    string `yaml:"api_key"`
		APISecret string `yaml:"api_secret"`
		BaseURL   string `yaml:"base_url"`
	} `yaml:"binance"`

	WebSocket struct {
		StreamURL        string        `yaml:"stream_url"`
		ListenKeyRefresh time.Duration `yaml:"listen_key_refresh"`
		ReconnectDelay   time.Duration `yaml:"reconnect_delay"`
		StartupLookback  time.Duration `yaml:"startup_lookback"`
	} `yaml:"websocket"`

	Destinations map[string]map[string]config.Destination `yaml:"destinations"`

	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`

	Server struct {
		Address string `yaml:"address"`
	} `yaml:"server"`

	Poller struct {
		Interval time.Duration `yaml:"interval"`
	} `yaml:"poller"`

	Logging struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	} `yaml:"logging"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	// Load configuration
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Set up logging
	logger := setupLogger(cfg)
	slog.SetDefault(logger)

	logger.Info("starting binance withdrawal service",
		"config", configPath,
	)

	// Expand environment variables in credentials
	apiKey := os.ExpandEnv(cfg.Binance.APIKey)
	apiSecret := os.ExpandEnv(cfg.Binance.APISecret)

	if apiKey == "" || apiSecret == "" {
		return fmt.Errorf("binance API credentials not configured")
	}

	// Open BoltDB store
	dbPath := cfg.Database.Path
	if dbPath == "" {
		dbPath = "./data/withdrawals.bolt"
	}
	logger.Info("opening database", "path", dbPath)

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	// Create Binance client
	var clientOpts []binance.ClientOption
	if cfg.Binance.BaseURL != "" {
		clientOpts = append(clientOpts, binance.WithBaseURL(cfg.Binance.BaseURL))
	}
	client := binance.NewClient(apiKey, apiSecret, clientOpts...)

	// Load destination configuration
	destCfg := config.NewDestinationConfig()
	for subAccount, assets := range cfg.Destinations {
		for asset, dest := range assets {
			if err := destCfg.SetDestination(subAccount, asset, dest); err != nil {
				return fmt.Errorf("invalid destination %s/%s: %w", subAccount, asset, err)
			}
		}
	}
	logger.Info("loaded destinations", "count", destCfg.Count())

	// Create audit logger
	auditLogger := audit.New(audit.WithLogger(logger))

	// Create forwarder
	fwd := forwarder.New(client, st, destCfg,
		forwarder.WithForwarderLogger(logger),
		forwarder.WithAuditLogger(auditLogger),
	)

	// Recover any pending transfers from a previous crash
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger.Info("recovering pending transfers")
	if err := fwd.RecoverPending(ctx); err != nil {
		logger.Warn("error recovering pending transfers", "error", err)
	}

	// Create transfer handler that forwards detected transfers
	transferHandler := func(transfer *model.TransferRecord) {
		result := fwd.Forward(ctx, transfer)
		logger.Info("transfer processed",
			"tran_id", transfer.TranID,
			"status", result.Status,
			"error", result.Error,
		)
	}

	// Create detector with WebSocket connection
	var detectorOpts []detector.DetectorOption
	detectorOpts = append(detectorOpts, detector.WithLogger(logger))
	if cfg.WebSocket.StartupLookback > 0 {
		detectorOpts = append(detectorOpts, detector.WithLookbackDuration(cfg.WebSocket.StartupLookback))
	}

	det := detector.New(client, transferHandler, detectorOpts...)

	// Start detector
	logger.Info("starting transfer detector")
	if err := det.Start(ctx); err != nil {
		return fmt.Errorf("starting detector: %w", err)
	}
	defer det.Stop()

	// Create and start completion poller
	var pollerOpts []poller.Option
	pollerOpts = append(pollerOpts, poller.WithLogger(logger))
	pollerOpts = append(pollerOpts, poller.WithAuditLogger(auditLogger))
	if cfg.Poller.Interval > 0 {
		pollerOpts = append(pollerOpts, poller.WithInterval(cfg.Poller.Interval))
	}

	completionPoller := poller.New(client, st, pollerOpts...)

	logger.Info("starting completion poller")
	if err := completionPoller.Start(ctx); err != nil {
		return fmt.Errorf("starting poller: %w", err)
	}
	defer completionPoller.Stop()

	// Create API server
	serverAddr := cfg.Server.Address
	if serverAddr == "" {
		serverAddr = ":8080"
	}

	apiDeps := &api.Dependencies{
		Store:    st,
		Detector: det,
	}
	apiServer := api.New(serverAddr, apiDeps, api.WithLogger(logger))

	logger.Info("starting API server", "address", serverAddr)
	if err := apiServer.Start(); err != nil {
		return fmt.Errorf("starting API server: %w", err)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	logger.Info("received shutdown signal", "signal", sig)

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	logger.Info("shutting down API server")
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("error shutting down API server", "error", err)
	}

	cancel() // Cancel the main context to stop detector and poller

	logger.Info("shutdown complete")
	return nil
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Expand environment variables in the config
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return &cfg, nil
}

func setupLogger(cfg *Config) *slog.Logger {
	var level slog.Level
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if cfg.Logging.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
