package discovery

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"github.com/nightjarctl/nightjar/internal/adapters"
	"github.com/nightjarctl/nightjar/internal/adapters/generic"
	"github.com/nightjarctl/nightjar/internal/indexer"
	"github.com/nightjarctl/nightjar/internal/types"
)

// Known policy-related API groups. Resources in these groups are always treated
// as constraint-like, regardless of heuristic matching.
var knownPolicyGroups = map[string]bool{
	"networking.k8s.io":            true,
	"cilium.io":                    true,
	"constraints.gatekeeper.sh":    true,
	"kyverno.io":                   true,
	"security.istio.io":            true,
	"networking.istio.io":          true,
	"admissionregistration.k8s.io": true,
	"policy":                       true, // PodSecurityPolicy (deprecated but may exist)
}

// Heuristic substrings in resource names that suggest a constraint-like resource.
var policyNameHints = []string{
	"policy", "policies",
	"constraint", "constraints",
	"rule", "rules",
	"quota", "quotas",
	"limit", "limits",
	"authorization",
}

// Engine discovers constraint-like resources in the cluster and manages
// dynamic informers for them.
type Engine struct {
	logger          *zap.Logger
	discoveryClient discovery.DiscoveryInterface
	dynamicClient   dynamic.Interface
	registry        *adapters.Registry
	indexer         *indexer.Indexer
	genericAdapter  *generic.Adapter

	mu              sync.RWMutex
	watchedGVRs     map[schema.GroupVersionResource]bool
	informerFactory dynamicinformer.DynamicSharedInformerFactory
	stopCh          chan struct{}
	informers       map[schema.GroupVersionResource]cache.SharedIndexInformer

	rescanInterval time.Duration
}

// NewEngine creates a new discovery engine.
func NewEngine(
	logger *zap.Logger,
	discoveryClient discovery.DiscoveryInterface,
	dynamicClient dynamic.Interface,
	registry *adapters.Registry,
	idx *indexer.Indexer,
	rescanInterval time.Duration,
) *Engine {
	return &Engine{
		logger:          logger.Named("discovery"),
		discoveryClient: discoveryClient,
		dynamicClient:   dynamicClient,
		registry:        registry,
		indexer:         idx,
		genericAdapter:  generic.New(),
		watchedGVRs:     make(map[schema.GroupVersionResource]bool),
		informers:       make(map[schema.GroupVersionResource]cache.SharedIndexInformer),
		stopCh:          make(chan struct{}),
		rescanInterval:  rescanInterval,
	}
}

// Start begins the discovery loop. It performs an initial scan, then
// rescans periodically. Call with a cancellable context.
func (e *Engine) Start(ctx context.Context) error {
	e.logger.Info("Starting discovery engine", zap.Duration("rescan_interval", e.rescanInterval))

	// Initial scan
	if err := e.scan(ctx); err != nil {
		return err
	}

	// Periodic rescan for newly installed CRDs
	go func() {
		ticker := time.NewTicker(e.rescanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := e.scan(ctx); err != nil {
					e.logger.Error("Periodic rescan failed", zap.Error(err))
				}
			}
		}
	}()

	return nil
}

// scan enumerates all API resources and identifies constraint-like ones.
func (e *Engine) scan(ctx context.Context) error {
	e.logger.Debug("Scanning for constraint-like resources")

	lists, err := e.discoveryClient.ServerPreferredResources()
	if err != nil {
		// ServerPreferredResources can return partial results with an error.
		// Log the error but continue with what we got.
		e.logger.Warn("Partial discovery result", zap.Error(err))
	}

	var discovered []schema.GroupVersionResource
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			e.logger.Warn("Failed to parse group version", zap.String("gv", list.GroupVersion), zap.Error(parseErr))
			continue
		}

		for _, r := range list.APIResources {
			// Skip sub-resources (e.g., pods/status, pods/log)
			if strings.Contains(r.Name, "/") {
				continue
			}

			gvr := schema.GroupVersionResource{
				Group:    gv.Group,
				Version:  gv.Version,
				Resource: r.Name,
			}

			if e.isConstraintLike(gvr, r.Name) {
				discovered = append(discovered, gvr)
			}
		}
	}

	e.logger.Info("Discovery scan complete",
		zap.Int("discovered", len(discovered)),
		zap.Int("previously_watched", len(e.watchedGVRs)),
	)

	// Start informers for newly discovered GVRs
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, gvr := range discovered {
		if !e.watchedGVRs[gvr] {
			e.logger.Info("New constraint-like resource discovered",
				zap.String("group", gvr.Group),
				zap.String("version", gvr.Version),
				zap.String("resource", gvr.Resource),
			)
			e.watchedGVRs[gvr] = true
			e.startInformer(ctx, gvr)
		}
	}

	return nil
}

// startInformer creates and starts a dynamic informer for the given GVR.
func (e *Engine) startInformer(ctx context.Context, gvr schema.GroupVersionResource) {
	// Create informer factory if not already created
	if e.informerFactory == nil {
		e.informerFactory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(
			e.dynamicClient,
			30*time.Minute, // resync period
			"",             // all namespaces
			nil,            // no tweaks
		)
	}

	informer := e.informerFactory.ForResource(gvr).Informer()

	// Register event handlers
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			e.handleAdd(ctx, gvr, obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			e.handleUpdate(ctx, gvr, newObj)
		},
		DeleteFunc: func(obj interface{}) {
			e.handleDelete(gvr, obj)
		},
	})

	e.informers[gvr] = informer

	// Start the informer in a goroutine
	go informer.Run(e.stopCh)

	e.logger.Debug("Started informer for GVR",
		zap.String("gvr", gvr.String()),
	)
}

// handleAdd processes a new object.
func (e *Engine) handleAdd(ctx context.Context, gvr schema.GroupVersionResource, obj interface{}) {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		e.logger.Warn("Unexpected object type in AddFunc")
		return
	}

	constraints, err := e.parseObject(ctx, gvr, unstructuredObj)
	if err != nil {
		e.logger.Error("Failed to parse object",
			zap.String("gvr", gvr.String()),
			zap.String("name", unstructuredObj.GetName()),
			zap.String("namespace", unstructuredObj.GetNamespace()),
			zap.Error(err),
		)
		return
	}

	for _, c := range constraints {
		e.indexer.Upsert(c)
	}
}

// handleUpdate processes an updated object.
func (e *Engine) handleUpdate(ctx context.Context, gvr schema.GroupVersionResource, obj interface{}) {
	// Treat updates the same as adds - upsert will replace existing
	e.handleAdd(ctx, gvr, obj)
}

// handleDelete processes a deleted object.
func (e *Engine) handleDelete(gvr schema.GroupVersionResource, obj interface{}) {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		// Handle deleted final state unknown (tombstone)
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			if unstructuredObj, ok = tombstone.Obj.(*unstructured.Unstructured); !ok {
				e.logger.Warn("Unexpected object type in tombstone")
				return
			}
		} else {
			e.logger.Warn("Unexpected object type in DeleteFunc")
			return
		}
	}

	e.indexer.Delete(unstructuredObj.GetUID())
}

// parseObject routes the object to the appropriate adapter.
func (e *Engine) parseObject(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) ([]types.Constraint, error) {
	// Try specific GVR adapter first
	adapter := e.registry.ForGVR(gvr)
	if adapter != nil {
		return adapter.Parse(ctx, obj)
	}

	// Try group-based matching (for dynamic CRDs like Gatekeeper constraints)
	adapter = e.registry.ForGroup(gvr.Group)
	if adapter != nil {
		return adapter.Parse(ctx, obj)
	}

	// Fall back to generic adapter
	return e.genericAdapter.ParseWithGVR(ctx, obj, gvr)
}

// isConstraintLike determines whether a GVR is likely a constraint/policy resource.
func (e *Engine) isConstraintLike(gvr schema.GroupVersionResource, resourceName string) bool {
	// Check 1: Is this a known policy group?
	if knownPolicyGroups[gvr.Group] {
		return true
	}

	// Check 2: Does the adapter registry already handle this GVR?
	if e.registry.ForGVR(gvr) != nil {
		return true
	}

	// Check 3: Native Kubernetes constraint resources
	if gvr.Group == "" {
		switch resourceName {
		case "resourcequotas", "limitranges":
			return true
		}
	}

	// Check 4: Heuristic — resource name contains policy-related substrings
	lower := strings.ToLower(resourceName)
	for _, hint := range policyNameHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}

	// Check 5: ConstraintProfile CRDs that register additional types
	// TODO: Check ConstraintProfile CRD instances

	// Check 6: CRD annotation override
	// TODO: Check for nightjar.io/is-policy annotation on CRD

	return false
}

// WatchedGVRs returns the set of GVRs currently being watched.
func (e *Engine) WatchedGVRs() []schema.GroupVersionResource {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]schema.GroupVersionResource, 0, len(e.watchedGVRs))
	for gvr := range e.watchedGVRs {
		result = append(result, gvr)
	}
	return result
}

// Stop stops all informers and the discovery engine.
func (e *Engine) Stop() {
	e.logger.Info("Stopping discovery engine")
	close(e.stopCh)
}
