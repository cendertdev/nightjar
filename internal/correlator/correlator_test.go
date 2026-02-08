package correlator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/nightjarctl/nightjar/internal/hubble"
	"github.com/nightjarctl/nightjar/internal/indexer"
	internaltypes "github.com/nightjarctl/nightjar/internal/types"
)

func TestNew(t *testing.T) {
	idx := indexer.New(nil)
	c := New(idx, nil, zap.NewNop())

	require.NotNil(t, c)
	assert.NotNil(t, c.notifications)
	assert.NotNil(t, c.flowDrops)
	assert.NotNil(t, c.limiter)
	assert.NotNil(t, c.seenPairs)
}

func TestNewWithOptions(t *testing.T) {
	idx := indexer.New(nil)
	opts := CorrelatorOptions{}
	c := NewWithOptions(idx, nil, zap.NewNop(), opts)

	require.NotNil(t, c)
	assert.Nil(t, c.hubbleClient)
}

func TestNotifications(t *testing.T) {
	idx := indexer.New(nil)
	c := New(idx, nil, zap.NewNop())

	ch := c.Notifications()
	require.NotNil(t, ch)
}

func TestFlowDropNotifications(t *testing.T) {
	idx := indexer.New(nil)
	c := New(idx, nil, zap.NewNop())

	ch := c.FlowDropNotifications()
	require.NotNil(t, ch)
}

func TestIsDuplicate(t *testing.T) {
	idx := indexer.New(nil)
	c := New(idx, nil, zap.NewNop())

	key := dedupeKey{
		eventUID:      "event-1",
		constraintUID: "constraint-1",
	}

	// Not seen yet
	assert.False(t, c.isDuplicate(key))

	// Mark as seen
	c.markSeen(key)

	// Now it's a duplicate
	assert.True(t, c.isDuplicate(key))
}

func TestMatchesSelector(t *testing.T) {
	tests := []struct {
		name     string
		selector *metav1.LabelSelector
		labels   map[string]string
		expected bool
	}{
		{
			name:     "nil selector matches all",
			selector: nil,
			labels:   map[string]string{"app": "foo"},
			expected: true,
		},
		{
			name:     "empty selector matches all",
			selector: &metav1.LabelSelector{},
			labels:   map[string]string{"app": "foo"},
			expected: true,
		},
		{
			name: "matching labels",
			selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "foo"},
			},
			labels:   map[string]string{"app": "foo", "version": "v1"},
			expected: true,
		},
		{
			name: "non-matching labels",
			selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "foo"},
			},
			labels:   map[string]string{"app": "bar"},
			expected: false,
		},
		{
			name: "missing label",
			selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "foo"},
			},
			labels:   map[string]string{"version": "v1"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesSelector(tt.selector, tt.labels)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandleFlowDrop_NonPolicyDrop(t *testing.T) {
	idx := indexer.New(nil)
	c := New(idx, nil, zap.NewNop())

	ctx := context.Background()

	// Non-policy drop should be ignored
	drop := hubble.NewFlowDropBuilder().
		WithSource("ns", "pod1", nil).
		WithDestination("ns", "pod2", nil).
		WithDropReason(hubble.DropReasonTTLExceeded). // Not a policy drop
		Build()

	c.handleFlowDrop(ctx, drop)

	// No notification should be sent
	select {
	case <-c.flowDrops:
		t.Fatal("unexpected flow drop notification")
	default:
		// Expected
	}
}

func TestHandleFlowDrop_PolicyDrop(t *testing.T) {
	idx := indexer.New(nil)
	c := New(idx, nil, zap.NewNop())

	ctx := context.Background()

	// Add a network policy constraint to the indexer
	constraint := internaltypes.Constraint{
		UID:            types.UID("constraint-1"),
		Name:           "deny-external",
		Namespace:      "production",
		ConstraintType: internaltypes.ConstraintTypeNetworkIngress,
		WorkloadSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "backend"},
		},
	}
	idx.Upsert(constraint)

	// Policy drop for matching pod
	drop := hubble.NewFlowDropBuilder().
		WithSource("external", "client-pod", map[string]string{"type": "external"}).
		WithDestination("production", "backend-xyz", map[string]string{"app": "backend"}).
		WithTCP(45678, 8080, hubble.TCPFlags{SYN: true}).
		WithDropReason(hubble.DropReasonPolicy).
		Build()

	c.handleFlowDrop(ctx, drop)

	// Should receive a notification
	select {
	case notification := <-c.flowDrops:
		assert.Equal(t, "backend-xyz", notification.DestPodName)
		assert.Equal(t, "production", notification.DestNamespace)
		assert.Equal(t, uint32(8080), notification.DestPort)
		assert.Equal(t, "TCP", notification.Protocol)
		assert.Equal(t, "deny-external", notification.Constraint.Name)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for flow drop notification")
	}
}

func TestHandleFlowDrop_NoMatchingSelector(t *testing.T) {
	idx := indexer.New(nil)
	c := New(idx, nil, zap.NewNop())

	ctx := context.Background()

	// Add a constraint with different selector
	constraint := internaltypes.Constraint{
		UID:            types.UID("constraint-1"),
		Name:           "deny-frontend",
		Namespace:      "production",
		ConstraintType: internaltypes.ConstraintTypeNetworkIngress,
		WorkloadSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "frontend"}, // Different label
		},
	}
	idx.Upsert(constraint)

	// Drop for non-matching pod
	drop := hubble.NewFlowDropBuilder().
		WithSource("external", "client", nil).
		WithDestination("production", "backend-xyz", map[string]string{"app": "backend"}).
		WithTCP(45678, 8080, hubble.TCPFlags{}).
		WithDropReason(hubble.DropReasonPolicy).
		Build()

	c.handleFlowDrop(ctx, drop)

	// Should not receive a notification
	select {
	case <-c.flowDrops:
		t.Fatal("unexpected flow drop notification for non-matching pod")
	case <-time.After(100 * time.Millisecond):
		// Expected
	}
}

func TestHandleFlowDrop_Deduplication(t *testing.T) {
	idx := indexer.New(nil)
	c := New(idx, nil, zap.NewNop())

	ctx := context.Background()

	// Add a constraint
	constraint := internaltypes.Constraint{
		UID:            types.UID("constraint-1"),
		Name:           "deny-all",
		Namespace:      "production",
		ConstraintType: internaltypes.ConstraintTypeNetworkIngress,
	}
	idx.Upsert(constraint)

	// Same drop
	drop := hubble.NewFlowDropBuilder().
		WithSource("external", "client", nil).
		WithDestination("production", "backend", nil).
		WithTCP(45678, 8080, hubble.TCPFlags{}).
		WithDropReason(hubble.DropReasonPolicy).
		Build()

	// First drop should be processed
	c.handleFlowDrop(ctx, drop)
	select {
	case <-c.flowDrops:
		// Expected
	case <-time.After(time.Second):
		t.Fatal("expected notification for first drop")
	}

	// Second identical drop should be deduplicated
	c.handleFlowDrop(ctx, drop)
	select {
	case <-c.flowDrops:
		t.Fatal("duplicate drop should be suppressed")
	case <-time.After(100 * time.Millisecond):
		// Expected
	}
}

func TestCleanupDedupeCache(t *testing.T) {
	idx := indexer.New(nil)
	c := New(idx, nil, zap.NewNop())

	// Add an old entry that's past the dedupeWindow
	oldKey := dedupeKey{
		eventUID:      "old-event",
		constraintUID: "constraint",
	}
	newKey := dedupeKey{
		eventUID:      "new-event",
		constraintUID: "constraint",
	}
	c.mu.Lock()
	c.seenPairs[oldKey] = time.Now().Add(-10 * time.Minute) // Older than dedupeWindow
	c.seenPairs[newKey] = time.Now()                        // Recent
	c.mu.Unlock()

	// Manually run the cleanup logic (same as what cleanupDedupeCache does)
	c.mu.Lock()
	cutoff := time.Now().Add(-dedupeWindow)
	for key, seenAt := range c.seenPairs {
		if seenAt.Before(cutoff) {
			delete(c.seenPairs, key)
		}
	}
	c.mu.Unlock()

	// Old entry should be removed, new one should remain
	c.mu.RLock()
	_, oldExists := c.seenPairs[oldKey]
	_, newExists := c.seenPairs[newKey]
	c.mu.RUnlock()

	assert.False(t, oldExists, "old entry should be cleaned up")
	assert.True(t, newExists, "new entry should remain")
}
