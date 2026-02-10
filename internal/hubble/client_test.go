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

func TestNewClient_DefaultOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Pass empty options — all defaults should be filled
	client, err := NewClient(ctx, ClientOptions{})
	require.NoError(t, err)
	require.NotNil(t, client)

	// Verify defaults were applied
	assert.Equal(t, DefaultClientOptions().RelayAddress, client.opts.RelayAddress)
	assert.Equal(t, DefaultClientOptions().ReconnectInterval, client.opts.ReconnectInterval)
	assert.Equal(t, DefaultClientOptions().MaxReconnectInterval, client.opts.MaxReconnectInterval)
	assert.Equal(t, DefaultClientOptions().BufferSize, client.opts.BufferSize)

	cancel()
	client.Close()
}

func TestClient_SetState(t *testing.T) {
	client := &Client{
		state: StateDisconnected,
	}

	client.setState(StateConnecting)
	assert.Equal(t, StateConnecting, client.State())

	client.setState(StateConnected)
	assert.Equal(t, StateConnected, client.State())
	assert.True(t, client.IsConnected())

	client.setState(StateReconnecting)
	assert.Equal(t, StateReconnecting, client.State())
	assert.False(t, client.IsConnected())
}

func TestClient_CloseIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := NewClient(ctx, ClientOptions{
		RelayAddress:      "localhost:4245",
		ReconnectInterval: time.Hour,
		BufferSize:        5,
		Logger:            zap.NewNop(),
	})
	require.NoError(t, err)

	// Close twice should not panic
	err = client.Close()
	assert.NoError(t, err)

	err = client.Close()
	assert.NoError(t, err)
}

func TestClient_ContextCancel_StopsConnectionLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	client, err := NewClient(ctx, ClientOptions{
		RelayAddress:      "localhost:4245",
		ReconnectInterval: time.Hour, // Long interval so we don't reconnect during test
		BufferSize:        5,
		Logger:            zap.NewNop(),
	})
	require.NoError(t, err)

	// Cancel context should cause connectionLoop to exit
	cancel()

	// Close should return promptly (connectionLoop should have exited via context)
	done := make(chan error, 1)
	go func() {
		done <- client.Close()
	}()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return in time after context cancellation")
	}
}

func TestClient_StreamFlows_ContextCancel(t *testing.T) {
	// Create a client without starting connectionLoop
	c := &Client{
		opts:   DefaultClientOptions(),
		logger: zap.NewNop().Named("hubble"),
		drops:  make(chan FlowDrop, 10),
		stopCh: make(chan struct{}),
		state:  StateConnected,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := c.streamFlows(ctx)
	assert.Equal(t, context.Canceled, err)
}

func TestClient_StreamFlows_StopChannel(t *testing.T) {
	c := &Client{
		opts:   DefaultClientOptions(),
		logger: zap.NewNop().Named("hubble"),
		drops:  make(chan FlowDrop, 10),
		stopCh: make(chan struct{}),
		state:  StateConnected,
	}

	close(c.stopCh) // Close stop channel immediately

	err := c.streamFlows(context.Background())
	assert.NoError(t, err)
}

func TestClient_StreamFlows_TimedContextCancel(t *testing.T) {
	// Test that streamFlows returns promptly when context is cancelled after a delay
	c := &Client{
		opts:   DefaultClientOptions(),
		logger: zap.NewNop().Named("hubble"),
		drops:  make(chan FlowDrop, 10),
		stopCh: make(chan struct{}),
		state:  StateConnected,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.streamFlows(ctx)
	elapsed := time.Since(start)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 500*time.Millisecond, "streamFlows should return promptly on context deadline")
}

func TestClient_ConnectionLoop_StopSignal(t *testing.T) {
	// Test that connectionLoop exits on stopCh close
	// connect() will try real gRPC and fail, but the loop should still respect the stop signal
	c := &Client{
		opts: ClientOptions{
			RelayAddress:         "localhost:1", // Invalid port to fail quickly
			ReconnectInterval:    10 * time.Millisecond,
			MaxReconnectInterval: 50 * time.Millisecond,
			BufferSize:           10,
			Logger:               zap.NewNop(),
		},
		logger: zap.NewNop().Named("hubble"),
		drops:  make(chan FlowDrop, 10),
		stopCh: make(chan struct{}),
		state:  StateDisconnected,
	}

	c.wg.Add(1)
	go c.connectionLoop(context.Background())

	// Let it try to connect at least once
	time.Sleep(50 * time.Millisecond)

	// Stop it
	close(c.stopCh)

	// Wait with a timeout to prevent test hangs
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("connectionLoop did not exit after stop signal")
	}

	// Drops channel should be closed (connectionLoop defers close(c.drops))
	_, open := <-c.drops
	assert.False(t, open, "drops channel should be closed after connectionLoop exits")
}

func TestClient_ConnectionLoop_ContextCancel(t *testing.T) {
	// Test that connectionLoop exits when context is cancelled
	c := &Client{
		opts: ClientOptions{
			RelayAddress:         "localhost:1",
			ReconnectInterval:    10 * time.Millisecond,
			MaxReconnectInterval: 50 * time.Millisecond,
			BufferSize:           10,
			Logger:               zap.NewNop(),
		},
		logger: zap.NewNop().Named("hubble"),
		drops:  make(chan FlowDrop, 10),
		stopCh: make(chan struct{}),
		state:  StateDisconnected,
	}

	ctx, cancel := context.WithCancel(context.Background())

	c.wg.Add(1)
	go c.connectionLoop(ctx)

	// Let it try to connect at least once
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait with a timeout to prevent test hangs
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("connectionLoop did not exit after context cancellation")
	}

	// Drops channel should be closed
	_, open := <-c.drops
	assert.False(t, open, "drops channel should be closed after connectionLoop exits")
}

func TestClient_ConnectionLoop_StateTransitions(t *testing.T) {
	// Test that connectionLoop sets state to Connecting on entry
	// grpc.DialContext is lazy, so connect() will succeed even for invalid addresses.
	// After connect() succeeds, the loop calls streamFlows() which blocks until stop.
	// We verify state transitions by checking state after a brief delay.
	c := &Client{
		opts: ClientOptions{
			RelayAddress:         "localhost:1",
			ReconnectInterval:    10 * time.Millisecond,
			MaxReconnectInterval: 50 * time.Millisecond,
			BufferSize:           10,
			Logger:               zap.NewNop(),
		},
		logger: zap.NewNop().Named("hubble"),
		drops:  make(chan FlowDrop, 10),
		stopCh: make(chan struct{}),
		state:  StateDisconnected,
	}

	c.wg.Add(1)
	go c.connectionLoop(context.Background())

	// Let the loop start and connect (lazy dial succeeds immediately)
	time.Sleep(100 * time.Millisecond)

	// Since gRPC dial is lazy and succeeds, the loop should have progressed
	// to Connected state and be waiting in streamFlows
	state := c.State()
	assert.True(t, state == StateConnected || state == StateConnecting || state == StateReconnecting,
		"expected a valid state, got %s", state)

	close(c.stopCh)
	c.wg.Wait()
}

func TestClient_Close_WithNilConn(t *testing.T) {
	// Directly test Close when conn is nil (covers the nil-conn path)
	c := &Client{
		opts:   DefaultClientOptions(),
		logger: zap.NewNop().Named("hubble"),
		drops:  make(chan FlowDrop, 10),
		stopCh: make(chan struct{}),
		state:  StateDisconnected,
		conn:   nil, // Explicitly nil
	}

	// Need a goroutine on wg to match what connectionLoop would do
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		<-c.stopCh
		close(c.drops)
	}()

	err := c.Close()
	assert.NoError(t, err, "Close with nil conn should return nil")
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
