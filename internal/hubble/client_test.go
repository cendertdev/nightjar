package hubble

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDefaultClientOptions(t *testing.T) {
	opts := DefaultClientOptions()

	assert.Equal(t, "hubble-relay.kube-system.svc.cluster.local:4245", opts.RelayAddress)
	assert.Equal(t, time.Second, opts.ReconnectInterval)
	assert.Equal(t, time.Minute, opts.MaxReconnectInterval)
	assert.Equal(t, 1000, opts.BufferSize)
}

func TestNewClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := ClientOptions{
		RelayAddress:      "localhost:4245",
		ReconnectInterval: 100 * time.Millisecond,
		BufferSize:        10,
		Logger:            zap.NewNop(),
	}

	client, err := NewClient(ctx, opts)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Client should start in disconnected state and try to connect
	// Since there's no actual Hubble server, it will be in connecting/disconnected state
	state := client.State()
	assert.True(t, state == StateDisconnected || state == StateConnecting || state == StateReconnecting,
		"expected initial state, got %s", state)

	// Close the client
	err = client.Close()
	assert.NoError(t, err)
}

func TestClient_DroppedFlows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := NewClient(ctx, ClientOptions{
		RelayAddress:      "localhost:4245",
		ReconnectInterval: 100 * time.Millisecond,
		BufferSize:        10,
		Logger:            zap.NewNop(),
	})
	require.NoError(t, err)

	// Get the drops channel
	dropsChan := client.DroppedFlows()
	require.NotNil(t, dropsChan)

	// Close should close the channel
	client.Close()

	// Channel should be closed
	_, open := <-dropsChan
	assert.False(t, open, "drops channel should be closed after Close()")
}

func TestClient_Stats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := NewClient(ctx, ClientOptions{
		RelayAddress:      "localhost:4245",
		ReconnectInterval: 100 * time.Millisecond,
		BufferSize:        10,
		Logger:            zap.NewNop(),
	})
	require.NoError(t, err)
	defer client.Close()

	stats := client.Stats()
	// Initial state, no reconnects yet or just starting
	assert.GreaterOrEqual(t, stats.Reconnects, uint64(0))
	assert.Equal(t, uint64(0), stats.FlowDrops)
}

func TestClient_IsConnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := NewClient(ctx, ClientOptions{
		RelayAddress:      "localhost:4245",
		ReconnectInterval: 100 * time.Millisecond,
		BufferSize:        10,
		Logger:            zap.NewNop(),
	})
	require.NoError(t, err)
	defer client.Close()

	// Should not be connected (no actual server)
	assert.False(t, client.IsConnected())
}

func TestClient_nextReconnectInterval(t *testing.T) {
	client := &Client{
		opts: ClientOptions{
			ReconnectInterval:    time.Second,
			MaxReconnectInterval: 30 * time.Second,
		},
	}

	// Test exponential backoff
	interval := client.nextReconnectInterval(time.Second)
	assert.Equal(t, 2*time.Second, interval)

	interval = client.nextReconnectInterval(2 * time.Second)
	assert.Equal(t, 4*time.Second, interval)

	interval = client.nextReconnectInterval(4 * time.Second)
	assert.Equal(t, 8*time.Second, interval)

	// Test max cap
	interval = client.nextReconnectInterval(20 * time.Second)
	assert.Equal(t, 30*time.Second, interval)

	interval = client.nextReconnectInterval(30 * time.Second)
	assert.Equal(t, 30*time.Second, interval)
}

func TestClient_recordFlowDrop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := NewClient(ctx, ClientOptions{
		RelayAddress:      "localhost:4245",
		ReconnectInterval: time.Hour, // Long interval to avoid reconnect noise
		BufferSize:        5,
		Logger:            zap.NewNop(),
	})
	require.NoError(t, err)
	defer client.Close()

	// Record a flow drop
	drop := NewFlowDropBuilder().
		WithSource("production", "frontend-abc", map[string]string{"app": "frontend"}).
		WithDestination("production", "backend-xyz", map[string]string{"app": "backend"}).
		WithTCP(45678, 8080, TCPFlags{SYN: true}).
		WithDropReason(DropReasonPolicy).
		Build()

	client.recordFlowDrop(drop)

	// Should be able to receive the drop
	select {
	case received := <-client.DroppedFlows():
		assert.Equal(t, "frontend-abc", received.Source.PodName)
		assert.Equal(t, "backend-xyz", received.Destination.PodName)
		assert.Equal(t, DropReasonPolicy, received.DropReason)
		assert.Equal(t, uint32(8080), received.L4.DestinationPort)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for flow drop")
	}

	// Check stats
	stats := client.Stats()
	assert.Equal(t, uint64(1), stats.FlowDrops)
}

func TestClient_recordFlowDrop_ChannelFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := NewClient(ctx, ClientOptions{
		RelayAddress:      "localhost:4245",
		ReconnectInterval: time.Hour,
		BufferSize:        2, // Small buffer
		Logger:            zap.NewNop(),
	})
	require.NoError(t, err)
	defer client.Close()

	drop := NewFlowDropBuilder().
		WithSource("ns", "pod1", nil).
		WithDestination("ns", "pod2", nil).
		Build()

	// Fill the buffer
	client.recordFlowDrop(drop)
	client.recordFlowDrop(drop)

	// This should not block, just drop the event
	client.recordFlowDrop(drop)

	// Only 2 drops should be recorded (buffer size)
	// The third one should be dropped
	stats := client.Stats()
	assert.Equal(t, uint64(2), stats.FlowDrops)
}
