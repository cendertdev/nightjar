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

func main() {
	var (
		tlsCertFile    string
		tlsKeyFile     string
		addr           string
		controllerURL  string
		namespace      string
		selfSignedMode bool
	)

	flag.StringVar(&tlsCertFile, "tls-cert-file", "", "Path to TLS certificate file (optional if using self-signed mode)")
	flag.StringVar(&tlsKeyFile, "tls-key-file", "", "Path to TLS key file (optional if using self-signed mode)")
	flag.StringVar(&addr, "addr", ":8443", "Address to listen on")
	flag.StringVar(&controllerURL, "controller-url", "http://nightjar-controller.nightjar-system.svc:8080", "URL of the Nightjar controller API")
	flag.StringVar(&namespace, "namespace", "nightjar-system", "Namespace where the webhook runs")
	flag.BoolVar(&selfSignedMode, "self-signed", true, "Use self-signed certificate management")
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

	logger.Info("Starting Nightjar admission webhook",
		zap.String("addr", addr),
		zap.String("controller_url", controllerURL),
		zap.Bool("self_signed", selfSignedMode),
	)

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

	// Get Kubernetes config
	config, err := rest.InClusterConfig()
	if err != nil {
		logger.Fatal("Failed to get in-cluster config", zap.Error(err))
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Fatal("Failed to create Kubernetes client", zap.Error(err))
	}

	// Setup certificate management
	var certManager *webhook.CertManager
	if selfSignedMode {
		certConfig := webhook.DefaultCertManagerConfig(namespace)
		certManager = webhook.NewCertManager(clientset, certConfig, logger)

		if err := certManager.EnsureCertificates(ctx); err != nil {
			logger.Fatal("Failed to ensure certificates", zap.Error(err))
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
	constraintClient := NewConstraintClient(controllerURL, logger)

	// Create admission handler
	handler := NewAdmissionHandler(constraintClient, logger)

	// Create and start server
	serverConfig := ServerConfig{
		Addr:        addr,
		TLSCertFile: tlsCertFile,
		TLSKeyFile:  tlsKeyFile,
		CertManager: certManager,
	}

	server := NewServer(serverConfig, handler, logger)
	if err := server.Start(ctx); err != nil {
		logger.Fatal("Server error", zap.Error(err))
	}

	logger.Info("Webhook server stopped")
}
