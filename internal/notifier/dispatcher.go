package notifier

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/nightjarctl/nightjar/internal/correlator"
	"github.com/nightjarctl/nightjar/internal/types"
)

// DispatcherOptions configures the Dispatcher behavior.
type DispatcherOptions struct {
	SuppressDuplicateMinutes int    // default 60
	RateLimitPerMinute       int    // default 100
	RemediationContact       string // shown in summary-level messages
}

// DefaultDispatcherOptions returns sensible defaults.
func DefaultDispatcherOptions() DispatcherOptions {
	return DispatcherOptions{
		SuppressDuplicateMinutes: 60,
		RateLimitPerMinute:       100,
		RemediationContact:       "your platform team",
	}
}

// dedupeKey uniquely identifies a constraint-workload notification pair.
type dedupeKey struct {
	constraintUID string
	workloadUID   string
}

// nsRateLimiter tracks rate limits per namespace.
type nsRateLimiter struct {
	mu         sync.Mutex
	limiters   map[string]*rate.Limiter
	lastAccess map[string]time.Time
	rate       rate.Limit
	burst      int
}

func newNsRateLimiter(perMinute int) *nsRateLimiter {
	return &nsRateLimiter{
		limiters:   make(map[string]*rate.Limiter),
		lastAccess: make(map[string]time.Time),
		rate:       rate.Limit(float64(perMinute) / 60.0),
		burst:      max(1, perMinute/10), // 10% burst, minimum 1
	}
}

func (n *nsRateLimiter) Allow(ns string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	limiter, exists := n.limiters[ns]
	if !exists {
		limiter = rate.NewLimiter(n.rate, n.burst)
		n.limiters[ns] = limiter
	}
	n.lastAccess[ns] = time.Now()
	return limiter.Allow()
}

// Evict removes namespace rate limiters that haven't been accessed within maxAge.
func (n *nsRateLimiter) Evict(maxAge time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for ns, last := range n.lastAccess {
		if last.Before(cutoff) {
			delete(n.limiters, ns)
			delete(n.lastAccess, ns)
		}
	}
}

// Dispatcher renders and dispatches constraint notifications.
type Dispatcher struct {
	logger       *zap.Logger
	client       kubernetes.Interface
	opts         DispatcherOptions
	nsLimiter    *nsRateLimiter
	eventBuilder *EventBuilder
	dedupeCache  map[dedupeKey]time.Time
	mu           sync.Mutex
}

// NewDispatcher creates a new Dispatcher.
func NewDispatcher(client kubernetes.Interface, logger *zap.Logger, opts DispatcherOptions) *Dispatcher {
	return &Dispatcher{
		logger:       logger.Named("dispatcher"),
		client:       client,
		opts:         opts,
		nsLimiter:    newNsRateLimiter(opts.RateLimitPerMinute),
		eventBuilder: NewEventBuilder(opts.RemediationContact),
		dedupeCache:  make(map[dedupeKey]time.Time),
	}
}

// Start begins the background cleanup routine. Non-blocking.
func (d *Dispatcher) Start(ctx context.Context) {
	go d.cleanupDedupeCache(ctx)
}

// Dispatch processes a correlated notification and sends it via enabled channels.
func (d *Dispatcher) Dispatch(ctx context.Context, n correlator.CorrelatedNotification) error {
	ns := n.Namespace

	// Rate limit per namespace
	if !d.nsLimiter.Allow(ns) {
		d.logger.Debug("Namespace rate limited", zap.String("namespace", ns))
		return nil
	}

	// Dedupe check (atomic check-and-mark to avoid TOCTOU race)
	key := dedupeKey{
		constraintUID: string(n.Constraint.UID),
		workloadUID:   fmt.Sprintf("%s/%s", ns, n.WorkloadName),
	}
	if !d.tryMarkSeen(key) {
		return nil
	}

	// Render message at summary level (per PRIVACY_MODEL.md: developer-facing events use summary)
	message := d.RenderMessage(n.Constraint, types.DetailLevelSummary)

	// Create K8s Event
	if err := d.createEvent(ctx, n, message); err != nil {
		d.logger.Error("Failed to create event", zap.Error(err))
		return err
	}

	d.logger.Info("Dispatched notification",
		zap.String("namespace", ns),
		zap.String("workload", n.WorkloadName),
		zap.String("constraint", n.Constraint.Name),
	)

	return nil
}

// DispatchDirect sends a notification for a constraint without a correlated event.
func (d *Dispatcher) DispatchDirect(ctx context.Context, c types.Constraint, ns, workloadName, workloadKind string, level types.DetailLevel) error {
	if !d.nsLimiter.Allow(ns) {
		return nil
	}

	key := dedupeKey{
		constraintUID: string(c.UID),
		workloadUID:   fmt.Sprintf("%s/%s", ns, workloadName),
	}
	if !d.tryMarkSeen(key) {
		return nil
	}

	message := d.RenderMessage(c, level)

	workload := WorkloadRef{
		Kind:      workloadKind,
		Name:      workloadName,
		Namespace: ns,
	}

	event := d.eventBuilder.BuildEvent(c, level, workload, message)

	_, err := d.client.CoreV1().Events(ns).Create(ctx, event, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

// RenderMessage formats a notification message at the specified detail level.
func (d *Dispatcher) RenderMessage(c types.Constraint, level types.DetailLevel) string {
	switch level {
	case types.DetailLevelFull:
		return d.renderFull(c)
	case types.DetailLevelDetailed:
		return d.renderDetailed(c)
	default:
		return d.renderSummary(c)
	}
}

// renderSummary creates a developer-safe notification without cross-namespace details.
func (d *Dispatcher) renderSummary(c types.Constraint) string {
	effect := genericEffect(c.ConstraintType)
	return fmt.Sprintf("⚠️ %s constraint is affecting your workload. %s. Contact %s for assistance.",
		c.ConstraintType, effect, d.opts.RemediationContact)
}

// renderDetailed includes constraint name and specific ports (same namespace only).
func (d *Dispatcher) renderDetailed(c types.Constraint) string {
	effect := c.Summary
	if effect == "" {
		effect = genericEffect(c.ConstraintType)
	}

	hint := c.RemediationHint
	if hint == "" {
		hint = fmt.Sprintf("Contact %s for assistance.", d.opts.RemediationContact)
	}

	return fmt.Sprintf("⚠️ %s constraint %q: %s. %s",
		c.ConstraintType, c.Name, effect, hint)
}

// renderFull includes all details including cross-namespace information.
func (d *Dispatcher) renderFull(c types.Constraint) string {
	source := fmt.Sprintf("%s/%s/%s", c.Source.Group, c.Source.Version, c.Source.Resource)
	if c.Source.Group == "" {
		source = fmt.Sprintf("core/%s/%s", c.Source.Version, c.Source.Resource)
	}

	location := c.Name
	if c.Namespace != "" {
		location = fmt.Sprintf("%s/%s", c.Namespace, c.Name)
	}

	return fmt.Sprintf("⚠️ [%s] %s %q: %s. %s",
		source, c.ConstraintType, location, c.Summary, c.RemediationHint)
}

// genericEffect returns a generic description of the constraint's effect.
func genericEffect(ct types.ConstraintType) string {
	switch ct {
	case types.ConstraintTypeNetworkIngress:
		return "Inbound network traffic is restricted"
	case types.ConstraintTypeNetworkEgress:
		return "Outbound network traffic is restricted"
	case types.ConstraintTypeAdmission:
		return "A validation policy may reject your resources"
	case types.ConstraintTypeResourceLimit:
		return "Resource quotas or limits apply"
	case types.ConstraintTypeMeshPolicy:
		return "Service mesh policies apply"
	case types.ConstraintTypeMissing:
		return "A required companion resource may be missing"
	default:
		return "A policy constraint applies"
	}
}

// createEvent creates a Kubernetes Event for the notification using EventBuilder
// to populate structured annotations for agent consumption.
func (d *Dispatcher) createEvent(ctx context.Context, n correlator.CorrelatedNotification, message string) error {
	workload := WorkloadRef{
		Kind:      n.WorkloadKind,
		Name:      n.WorkloadName,
		Namespace: n.Namespace,
	}

	event := d.eventBuilder.BuildEvent(n.Constraint, types.DetailLevelSummary, workload, message)

	_, err := d.client.CoreV1().Events(n.Namespace).Create(ctx, event, metav1.CreateOptions{})
	return err
}

// tryMarkSeen atomically checks if this constraint-workload pair was recently
// notified and, if not, marks it as seen. Returns true if this is a new (non-duplicate)
// notification that should be dispatched.
func (d *Dispatcher) tryMarkSeen(key dedupeKey) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if seenAt, exists := d.dedupeCache[key]; exists {
		window := time.Duration(d.opts.SuppressDuplicateMinutes) * time.Minute
		if time.Since(seenAt) < window {
			return false
		}
	}
	d.dedupeCache[key] = time.Now()
	return true
}

// cleanupDedupeCache periodically removes old entries.
func (d *Dispatcher) cleanupDedupeCache(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.mu.Lock()
			window := time.Duration(d.opts.SuppressDuplicateMinutes) * time.Minute
			cutoff := time.Now().Add(-window)
			for key, seenAt := range d.dedupeCache {
				if seenAt.Before(cutoff) {
					delete(d.dedupeCache, key)
				}
			}
			d.mu.Unlock()

			// Evict stale namespace rate limiters (namespaces not seen in 1 hour).
			d.nsLimiter.Evict(time.Hour)
		}
	}
}
