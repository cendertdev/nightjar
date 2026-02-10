package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/nightjarctl/nightjar/internal/webhook"
)

// runConfig holds parsed configuration for the webhook.
type runConfig struct {
	TLSCertFile    string
	TLSKeyFile     string
	Addr           string
	ControllerURL  string
	Namespace      string
	SelfSignedMode bool
}

func main() {
	cfg := runConfig{}
	flag.StringVar(&cfg.TLSCertFile, "tls-cert-file", "", "Path to TLS certificate file (optional if using self-signed mode)")
	flag.StringVar(&cfg.TLSKeyFile, "tls-key-file", "", "Path to TLS key file (optional if using self-signed mode)")
	flag.StringVar(&cfg.Addr, "addr", ":8443", "Address to listen on")
	flag.StringVar(&cfg.ControllerURL, "controller-url", "http://nightjar-controller.nightjar-system.svc:8080", "URL of the Nightjar controller API")
	flag.StringVar(&cfg.Namespace, "namespace", "nightjar-system", "Namespace where the webhook runs")
	flag.BoolVar(&cfg.SelfSignedMode, "self-signed", true, "Use self-signed certificate management")
	flag.Parse()

	// Setup logger
	logConfig := zap.NewProductionConfig()
	logConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	logger, err := logConfig.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	if err := run(cfg, logger); err != nil {
		logger.Fatal("Server error", zap.Error(err))
	}
}

// run contains the main application logic, separated from main() for testability.
func run(cfg runConfig, logger *zap.Logger) error {
	logger.Info("Starting Nightjar admission webhook",
		zap.String("addr", cfg.Addr),
		zap.String("controller_url", cfg.ControllerURL),
		zap.Bool("self_signed", cfg.SelfSignedMode),
	)

	// Get Kubernetes config
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return startServer(cfg, clientset, logger)
}

// startServer sets up the certificate manager, constraint client, admission
// handler, and HTTPS server. It blocks until the context is cancelled or an
// error occurs. Extracted from run() to allow testing with a fake clientset.
func startServer(cfg runConfig, clientset kubernetes.Interface, logger *zap.Logger) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("Received shutdown signal", zap.String("signal", sig.String()))
		cancel()
	}()

	// Setup certificate management
	var certManager *webhook.CertManager
	if cfg.SelfSignedMode {
		certConfig := webhook.DefaultCertManagerConfig(cfg.Namespace)
		certManager = webhook.NewCertManager(clientset, certConfig, logger)

		if err := certManager.EnsureCertificates(ctx); err != nil {
			return fmt.Errorf("failed to ensure certificates: %w", err)
		}

		// Update webhook configuration with CA bundle
		if err := certManager.UpdateWebhookCABundle(ctx); err != nil {
			logger.Error("Failed to update webhook CA bundle", zap.Error(err))
			// Continue anyway - the webhook config might not exist yet
		}

		// Start certificate rotation watcher
		certManager.StartRotationWatcher(ctx, 24*time.Hour)
	}

	// Create constraint client
	constraintClient := NewConstraintClient(cfg.ControllerURL, logger)

	// Create admission handler
	handler := NewAdmissionHandler(constraintClient, logger)

	// Create and start server
	serverConfig := ServerConfig{
		Addr:        cfg.Addr,
		TLSCertFile: cfg.TLSCertFile,
		TLSKeyFile:  cfg.TLSKeyFile,
		CertManager: certManager,
	}

	server := NewServer(serverConfig, handler, logger)
	if err := server.Start(ctx); err != nil {
		return err
	}

	logger.Info("Webhook server stopped")
	return nil
}
