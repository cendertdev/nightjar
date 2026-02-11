package main

import (
	"context"
	"flag"
	"os"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/nightjarctl/nightjar/internal/adapters"
	"github.com/nightjarctl/nightjar/internal/adapters/gatekeeper"
	"github.com/nightjarctl/nightjar/internal/adapters/kyverno"
	"github.com/nightjarctl/nightjar/internal/adapters/limitrange"
	"github.com/nightjarctl/nightjar/internal/adapters/networkpolicy"
	"github.com/nightjarctl/nightjar/internal/adapters/resourcequota"
	"github.com/nightjarctl/nightjar/internal/adapters/webhookconfig"
	"github.com/nightjarctl/nightjar/internal/correlator"
	discoveryengine "github.com/nightjarctl/nightjar/internal/discovery"
	"github.com/nightjarctl/nightjar/internal/hubble"
	"github.com/nightjarctl/nightjar/internal/indexer"
	"github.com/nightjarctl/nightjar/internal/mcp"
	"github.com/nightjarctl/nightjar/internal/notifier"
	"github.com/nightjarctl/nightjar/internal/requirements"
	"github.com/nightjarctl/nightjar/internal/types"
)

func main() {
	var (
		metricsAddr    string
		healthAddr     string
		leaderElect    bool
		rescanInterval time.Duration
		hubbleAddr     string
		hubbleEnabled  bool
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&healthAddr, "health-probe-bind-address", ":8081", "The address the health probe endpoint binds to.")
	flag.BoolVar(&leaderElect, "leader-elect", true, "Enable leader election for controller manager.")
	flag.DurationVar(&rescanInterval, "rescan-interval", 5*time.Minute, "How often to rescan for new CRDs.")
	flag.StringVar(&hubbleAddr, "hubble-relay-address", "hubble-relay.kube-system.svc:4245", "Hubble Relay gRPC address.")
	flag.BoolVar(&hubbleEnabled, "hubble-enabled", false, "Enable Hubble flow observation for real-time traffic drop detection.")
	flag.Parse()

	// Setup logger
	logConfig := zap.NewProductionConfig()
	logConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	logger, err := logConfig.Build()
	if err != nil {
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("Starting Nightjar",
		zap.String("version", "dev"),
		zap.Bool("leader_elect", leaderElect),
		zap.Duration("rescan_interval", rescanInterval),
		zap.Bool("hubble_enabled", hubbleEnabled),
	)

	// Setup controller-runtime manager
	cfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		LeaderElection:         leaderElect,
		LeaderElectionID:       "nightjar-leader",
		HealthProbeBindAddress: healthAddr,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
	})
	if err != nil {
		logger.Fatal("Unable to create manager", zap.Error(err))
	}

	// Register health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Fatal("Unable to set up health check", zap.Error(err))
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Fatal("Unable to set up readiness check", zap.Error(err))
	}

	// Build adapter registry
	registry := adapters.NewRegistry()
	mustRegister(logger, registry, networkpolicy.New())
	mustRegister(logger, registry, resourcequota.New())
	mustRegister(logger, registry, limitrange.New())
	mustRegister(logger, registry, webhookconfig.New())
	mustRegister(logger, registry, gatekeeper.New())
	mustRegister(logger, registry, kyverno.New())

	logger.Info("Adapter registry initialized",
		zap.Int("adapter_count", len(registry.All())),
		zap.Int("handled_gvrs", len(registry.HandledGVRs())),
	)

	// Build clients
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		logger.Fatal("Failed to create discovery client", zap.Error(err))
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		logger.Fatal("Failed to create dynamic client", zap.Error(err))
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Fatal("Failed to create clientset", zap.Error(err))
	}

	// Build constraint indexer with annotator callback
	var annotatorRef atomic.Pointer[notifier.WorkloadAnnotator]
	idx := indexer.New(func(event indexer.IndexEvent) {
		logger.Debug("Index event",
			zap.String("type", event.Type),
			zap.String("constraint", event.Constraint.Name),
		)
		if a := annotatorRef.Load(); a != nil {
			a.OnIndexChange(event)
		}
	})

	// Build discovery engine
	engine := discoveryengine.NewEngine(
		logger,
		discoveryClient,
		dynamicClient,
		registry,
		idx,
		rescanInterval,
	)

	// Build Hubble client (optional)
	var hubbleClient *hubble.Client
	if hubbleEnabled {
		var clientErr error
		hubbleClient, clientErr = hubble.NewClient(context.Background(), hubble.ClientOptions{
			RelayAddress: hubbleAddr,
			Logger:       logger,
		})
		if clientErr != nil {
			logger.Fatal("Failed to create Hubble client", zap.Error(clientErr))
		}
		logger.Info("Hubble client created", zap.String("relay_address", hubbleAddr))
	}

	// Build correlator
	corr := correlator.NewWithOptions(idx, clientset, logger, correlator.CorrelatorOptions{
		HubbleClient: hubbleClient,
	})

	// Build notification dispatcher
	dispatcherOpts := notifier.DefaultDispatcherOptions()
	dispatcher := notifier.NewDispatcher(clientset, logger, dispatcherOpts)

	// Build workload annotator
	annotatorOpts := notifier.DefaultWorkloadAnnotatorOptions()
	annotator := notifier.NewWorkloadAnnotator(dynamicClient, idx, logger, annotatorOpts)
	annotatorRef.Store(annotator)

	// Build requirements evaluator context
	evalCtx := requirements.NewDynamicEvalContext(dynamicClient)

	// MCP evaluator: debounce=0 for immediate pre-check responses.
	mcpEvaluator := requirements.NewEvaluator(idx, evalCtx, logger)
	mcpEvaluator.SetDebounceDuration(0)
	registerRequirementRules(mcpEvaluator)

	// Report reconciler evaluator: default 120s debounce.
	reconcilerEvaluator := requirements.NewEvaluator(idx, evalCtx, logger)
	registerRequirementRules(reconcilerEvaluator)

	// Build MCP server
	mcpOpts := mcp.DefaultServerOptions()
	mcpOpts.Logger = logger
	mcpOpts.Evaluator = mcpEvaluator
	mcpServer := mcp.NewServer(idx, mcpOpts)

	// Build report reconciler
	reconcilerOpts := notifier.DefaultReportReconcilerOptions()
	reportReconciler := notifier.NewReportReconciler(
		mgr.GetClient(), idx, logger, reconcilerOpts,
		reconcilerEvaluator, dynamicClient,
	)

	// Setup signal handler context
	ctx := ctrl.SetupSignalHandler()

	// Add runnable to start discovery engine
	if err := mgr.Add(&runnableFunc{fn: func(ctx context.Context) error {
		return engine.Start(ctx)
	}}); err != nil {
		logger.Fatal("Failed to add discovery engine to manager", zap.Error(err))
	}

	// Add runnable to start correlator
	if err := mgr.Add(&runnableFunc{fn: func(ctx context.Context) error {
		return corr.Start(ctx)
	}}); err != nil {
		logger.Fatal("Failed to add correlator to manager", zap.Error(err))
	}

	// Add runnable to start workload annotator
	if err := mgr.Add(&runnableFunc{fn: func(ctx context.Context) error {
		return annotator.Start(ctx)
	}}); err != nil {
		logger.Fatal("Failed to add workload annotator to manager", zap.Error(err))
	}

	// Add runnable to start dispatcher loop
	if err := mgr.Add(&runnableFunc{fn: func(ctx context.Context) error {
		dispatcher.Start(ctx)
		// Process notifications from correlator
		for {
			select {
			case <-ctx.Done():
				return nil
			case notification, ok := <-corr.Notifications():
				if !ok {
					return nil
				}
				if err := dispatcher.Dispatch(ctx, notification); err != nil {
					logger.Error("Failed to dispatch notification", zap.Error(err))
				}
			}
		}
	}}); err != nil {
		logger.Fatal("Failed to add dispatcher to manager", zap.Error(err))
	}

	// Add runnable to log flow drop notifications (consumer for Hubble correlation)
	if hubbleEnabled {
		if err := mgr.Add(&runnableFunc{fn: func(ctx context.Context) error {
			for {
				select {
				case <-ctx.Done():
					return nil
				case notification, ok := <-corr.FlowDropNotifications():
					if !ok {
						return nil
					}
					logger.Info("Flow drop correlated",
						zap.String("source_pod", notification.SourcePodName),
						zap.String("dest_pod", notification.DestPodName),
						zap.String("constraint", notification.Constraint.Name),
						zap.Uint32("dest_port", notification.DestPort),
						zap.String("protocol", notification.Protocol),
					)
				}
			}
		}}); err != nil {
			logger.Fatal("Failed to add flow drop consumer to manager", zap.Error(err))
		}
	}

	// Add runnable to start MCP server
	if err := mgr.Add(&runnableFunc{fn: func(ctx context.Context) error {
		return mcpServer.Start(ctx)
	}}); err != nil {
		logger.Fatal("Failed to add MCP server to manager", zap.Error(err))
	}

	// Add runnable to start report reconciler
	if err := mgr.Add(&runnableFunc{fn: func(ctx context.Context) error {
		return reportReconciler.Start(ctx)
	}}); err != nil {
		logger.Fatal("Failed to add report reconciler to manager", zap.Error(err))
	}

	// Add runnable for evaluator cleanup
	if err := mgr.Add(&runnableFunc{fn: func(ctx context.Context) error {
		reconcilerEvaluator.StartCleanup(ctx)
		return nil
	}}); err != nil {
		logger.Fatal("Failed to add evaluator cleanup to manager", zap.Error(err))
	}

	// Start manager (blocks until context is cancelled)
	logger.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		logger.Fatal("Manager exited with error", zap.Error(err))
	}

	// Cleanup
	if hubbleClient != nil {
		if err := hubbleClient.Close(); err != nil {
			logger.Error("Failed to close Hubble client", zap.Error(err))
		}
	}
	engine.Stop()
}

// registerRequirementRules registers all built-in requirement rules on the evaluator.
func registerRequirementRules(eval *requirements.Evaluator) {
	eval.RegisterRule(requirements.NewPrometheusMonitorRule())
	eval.RegisterRule(requirements.NewIstioRoutingRule())
	eval.RegisterRule(requirements.NewIstioMTLSRule())
	eval.RegisterRule(requirements.NewCertIssuerRule())
}

// mustRegister registers an adapter or exits on failure.
func mustRegister(logger *zap.Logger, registry *adapters.Registry, adapter types.Adapter) {
	if err := registry.Register(adapter); err != nil {
		logger.Fatal("Failed to register adapter",
			zap.String("adapter", adapter.Name()),
			zap.Error(err),
		)
	}
}

// runnableFunc is a helper to convert a function to a controller-runtime Runnable.
type runnableFunc struct {
	fn func(context.Context) error
}

func (r *runnableFunc) Start(ctx context.Context) error {
	return r.fn(ctx)
}
