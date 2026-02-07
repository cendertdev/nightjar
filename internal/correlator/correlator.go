package correlator

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/nightjarctl/nightjar/internal/indexer"
	"github.com/nightjarctl/nightjar/internal/types"
)

const (
	// Rate limit: 100 events/second
	eventRateLimit = 100
	eventRateBurst = 200

	// Deduplication window: 5 minutes
	dedupeWindow = 5 * time.Minute

	// Channel buffer size
	notificationBuffer = 1000
)

// CorrelatedNotification pairs a Kubernetes event with a matching constraint.
type CorrelatedNotification struct {
	Event        *corev1.Event
	Constraint   types.Constraint
	Namespace    string
	WorkloadName string
	WorkloadKind string
}

// dedupeKey uniquely identifies an event-constraint pair.
type dedupeKey struct {
	eventUID      string
	constraintUID string
}

// Correlator watches Kubernetes Warning events and correlates them with constraints.
type Correlator struct {
	logger        *zap.Logger
	client        kubernetes.Interface
	indexer       *indexer.Indexer
	notifications chan CorrelatedNotification
	limiter       *rate.Limiter

	mu        sync.RWMutex
	seenPairs map[dedupeKey]time.Time
}

// New creates a new Correlator.
func New(idx *indexer.Indexer, client kubernetes.Interface, logger *zap.Logger) *Correlator {
	return &Correlator{
		logger:        logger.Named("correlator"),
		client:        client,
		indexer:       idx,
		notifications: make(chan CorrelatedNotification, notificationBuffer),
		limiter:       rate.NewLimiter(eventRateLimit, eventRateBurst),
		seenPairs:     make(map[dedupeKey]time.Time),
	}
}

// Notifications returns the channel of correlated notifications.
func (c *Correlator) Notifications() <-chan CorrelatedNotification {
	return c.notifications
}

// Start begins watching events and correlating them. Blocks until context is cancelled.
func (c *Correlator) Start(ctx context.Context) error {
	c.logger.Info("Starting correlator")

	// Start dedupe cleaner
	go c.cleanupDedupeCache(ctx)

	for {
		if err := c.watchEvents(ctx); err != nil {
			if ctx.Err() != nil {
				c.logger.Info("Correlator stopped")
				close(c.notifications)
				return nil
			}
			c.logger.Error("Event watch failed, retrying", zap.Error(err))
			time.Sleep(5 * time.Second)
		}
	}
}

// watchEvents creates a watch on Warning events and processes them.
func (c *Correlator) watchEvents(ctx context.Context) error {
	watcher, err := c.client.CoreV1().Events("").Watch(ctx, metav1.ListOptions{
		FieldSelector: "type=Warning",
	})
	if err != nil {
		return err
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return nil // watch closed, will be retried
			}
			if event.Type == watch.Added || event.Type == watch.Modified {
				c.handleEvent(ctx, event.Object.(*corev1.Event))
			}
		}
	}
}

// handleEvent processes a single Kubernetes event.
func (c *Correlator) handleEvent(ctx context.Context, event *corev1.Event) {
	// Rate limit
	if !c.limiter.Allow() {
		c.logger.Debug("Event rate limited", zap.String("event", event.Name))
		return
	}

	involved := event.InvolvedObject
	ns := involved.Namespace
	if ns == "" {
		return // Skip cluster-scoped objects for now
	}

	// Query constraints for this namespace
	constraints := c.indexer.ByNamespace(ns)
	if len(constraints) == 0 {
		return
	}

	// Try to match each constraint
	for _, constraint := range constraints {
		// Dedupe check
		key := dedupeKey{
			eventUID:      string(event.UID),
			constraintUID: string(constraint.UID),
		}
		if c.isDuplicate(key) {
			continue
		}

		// For now, emit all constraints in the namespace
		// Future: add smarter matching based on event message, reason, etc.
		notification := CorrelatedNotification{
			Event:        event.DeepCopy(),
			Constraint:   constraint,
			Namespace:    ns,
			WorkloadName: involved.Name,
			WorkloadKind: involved.Kind,
		}

		select {
		case c.notifications <- notification:
			c.markSeen(key)
		case <-ctx.Done():
			return
		default:
			c.logger.Warn("Notification channel full, dropping event")
		}
	}
}

// isDuplicate checks if this event-constraint pair was recently processed.
func (c *Correlator) isDuplicate(key dedupeKey) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if seenAt, exists := c.seenPairs[key]; exists {
		return time.Since(seenAt) < dedupeWindow
	}
	return false
}

// markSeen records that an event-constraint pair was processed.
func (c *Correlator) markSeen(key dedupeKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seenPairs[key] = time.Now()
}

// cleanupDedupeCache periodically removes old entries from the dedupe cache.
func (c *Correlator) cleanupDedupeCache(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			cutoff := time.Now().Add(-dedupeWindow)
			for key, seenAt := range c.seenPairs {
				if seenAt.Before(cutoff) {
					delete(c.seenPairs, key)
				}
			}
			c.mu.Unlock()
		}
	}
}
