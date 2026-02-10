package requirements

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/nightjarctl/nightjar/internal/indexer"
	"github.com/nightjarctl/nightjar/internal/types"
)

const defaultDebounceDuration = 120 * time.Second

// Evaluator runs RequirementRules against workloads to detect missing companion resources.
// Rules must be registered before Evaluate is called (not concurrent with RegisterRule).
type Evaluator struct {
	indexer  *indexer.Indexer
	evalCtx  types.RequirementEvalContext
	logger   *zap.Logger
	rules    []types.RequirementRule
	clock    func() time.Time
	debounce debouncState
}

type debouncState struct {
	mu       sync.RWMutex
	duration time.Duration
	// firstSeen maps "workloadUID:ruleName" → time first detected missing.
	firstSeen map[string]time.Time
}

// NewEvaluator creates a new Evaluator. The indexer is stored for future use by
// downstream consumers. Rules must be registered via RegisterRule before calling
// Evaluate.
func NewEvaluator(idx *indexer.Indexer, evalCtx types.RequirementEvalContext, logger *zap.Logger) *Evaluator {
	return &Evaluator{
		indexer: idx,
		evalCtx: evalCtx,
		logger:  logger,
		clock:   time.Now,
		debounce: debouncState{
			duration:  defaultDebounceDuration,
			firstSeen: make(map[string]time.Time),
		},
	}
}

// SetClock overrides the time source. Must be called before Evaluate (not concurrent).
func (e *Evaluator) SetClock(clock func() time.Time) {
	e.clock = clock
}

// SetDebounceDuration overrides the debounce window (for testing).
func (e *Evaluator) SetDebounceDuration(d time.Duration) {
	e.debounce.mu.Lock()
	e.debounce.duration = d
	e.debounce.mu.Unlock()
}

// RegisterRule adds a rule. Must be called before Evaluate (not concurrent).
func (e *Evaluator) RegisterRule(rule types.RequirementRule) {
	e.rules = append(e.rules, rule)
}

// Evaluate runs all registered rules against the workload and returns constraints
// for resources that have been missing longer than the debounce window.
func (e *Evaluator) Evaluate(ctx context.Context, workload *unstructured.Unstructured) ([]types.Constraint, error) {
	if workload == nil {
		return nil, nil
	}

	workloadUID := string(workload.GetUID())
	if workloadUID == "" {
		workloadUID = workload.GetNamespace() + "/" + workload.GetName()
	}
	now := e.clock()

	var result []types.Constraint
	for _, rule := range e.rules {
		constraints, err := rule.Evaluate(ctx, workload, e.evalCtx)
		if err != nil {
			e.logger.Warn("Rule evaluation failed",
				zap.String("rule", rule.Name()),
				zap.String("workload", workloadUID),
				zap.Error(err),
			)
			continue
		}

		key := fmt.Sprintf("%s:%s", workloadUID, rule.Name())

		if len(constraints) == 0 {
			// Resource appeared — clear debounce entry.
			e.debounce.mu.Lock()
			delete(e.debounce.firstSeen, key)
			e.debounce.mu.Unlock()
			continue
		}

		// Record first-seen if not already tracked.
		e.debounce.mu.Lock()
		firstSeen, exists := e.debounce.firstSeen[key]
		if !exists {
			e.debounce.firstSeen[key] = now
			firstSeen = now
		}
		duration := e.debounce.duration
		e.debounce.mu.Unlock()

		// Only emit after debounce window has elapsed.
		if now.Sub(firstSeen) >= duration {
			result = append(result, constraints...)
		}
	}

	return result, nil
}

// CleanupStaleEntries removes debounce entries older than 2x the debounce duration.
// Call this periodically from a goroutine.
func (e *Evaluator) CleanupStaleEntries() {
	now := e.clock()

	e.debounce.mu.Lock()
	defer e.debounce.mu.Unlock()

	cutoff := 2 * e.debounce.duration
	for key, firstSeen := range e.debounce.firstSeen {
		if now.Sub(firstSeen) > cutoff {
			delete(e.debounce.firstSeen, key)
		}
	}
}

// StartCleanup runs periodic stale-entry cleanup until the context is cancelled.
func (e *Evaluator) StartCleanup(ctx context.Context) {
	e.debounce.mu.RLock()
	d := e.debounce.duration
	e.debounce.mu.RUnlock()

	ticker := time.NewTicker(d)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.CleanupStaleEntries()
		}
	}
}
