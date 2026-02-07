package adapters

import (
	"fmt"
	"sync"

	"github.com/nightjarctl/nightjar/internal/types"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Registry maintains the mapping of GVRs to adapters.
// It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]types.Adapter                      // name → adapter
	gvrMap   map[schema.GroupVersionResource]types.Adapter // GVR → adapter
}

// NewRegistry creates an empty adapter registry.
// Call Register() to add adapters, then use in the discovery engine.
func NewRegistry() *Registry {
	return &Registry{
		adapters: make(map[string]types.Adapter),
		gvrMap:   make(map[schema.GroupVersionResource]types.Adapter),
	}
}

// Register adds an adapter to the registry.
// It maps all GVRs returned by adapter.Handles() to this adapter.
// Returns an error if a GVR is already registered to a different adapter.
func (r *Registry) Register(adapter types.Adapter) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := adapter.Name()
	if _, exists := r.adapters[name]; exists {
		return fmt.Errorf("adapter %q already registered", name)
	}

	for _, gvr := range adapter.Handles() {
		if existing, exists := r.gvrMap[gvr]; exists {
			return fmt.Errorf("GVR %s already registered to adapter %q, cannot register to %q",
				gvr.String(), existing.Name(), name)
		}
		r.gvrMap[gvr] = adapter
	}

	r.adapters[name] = adapter
	return nil
}

// ForGVR returns the adapter registered for the given GVR, or nil if none.
func (r *Registry) ForGVR(gvr schema.GroupVersionResource) types.Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gvrMap[gvr]
}

// ForName returns the adapter with the given name, or nil if none.
func (r *Registry) ForName(name string) types.Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.adapters[name]
}

// All returns all registered adapters.
func (r *Registry) All() []types.Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]types.Adapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		result = append(result, a)
	}
	return result
}

// HandledGVRs returns all GVRs that have a registered adapter.
func (r *Registry) HandledGVRs() []schema.GroupVersionResource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]schema.GroupVersionResource, 0, len(r.gvrMap))
	for gvr := range r.gvrMap {
		result = append(result, gvr)
	}
	return result
}
