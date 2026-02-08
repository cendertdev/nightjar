package hubble

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFlowDropBuilder(t *testing.T) {
	now := time.Now()
	drop := NewFlowDropBuilder().
		WithTime(now).
		WithTraceID("trace-123").
		WithSource("production", "frontend-abc", map[string]string{"app": "frontend"}).
		WithSourceIdentity(12345).
		WithSourceWorkload("Deployment", "frontend").
		WithDestination("production", "backend-xyz", map[string]string{"app": "backend"}).
		WithDestinationIdentity(67890).
		WithDestinationWorkload("Deployment", "backend").
		WithIP("10.0.1.5", "10.0.2.10").
		WithTCP(45678, 8080, TCPFlags{SYN: true}).
		WithDropReason(DropReasonPolicy).
		WithPolicyName("deny-external").
		Build()

	assert.Equal(t, now, drop.Time)
	assert.Equal(t, "trace-123", drop.TraceID)

	// Source
	assert.Equal(t, "production", drop.Source.Namespace)
	assert.Equal(t, "frontend-abc", drop.Source.PodName)
	assert.Equal(t, uint32(12345), drop.Source.Identity)
	assert.Equal(t, "frontend", drop.Source.Labels["app"])
	assert.Len(t, drop.Source.Workloads, 1)
	assert.Equal(t, "Deployment", drop.Source.Workloads[0].Kind)
	assert.Equal(t, "frontend", drop.Source.Workloads[0].Name)

	// Destination
	assert.Equal(t, "production", drop.Destination.Namespace)
	assert.Equal(t, "backend-xyz", drop.Destination.PodName)
	assert.Equal(t, uint32(67890), drop.Destination.Identity)
	assert.Equal(t, "backend", drop.Destination.Labels["app"])
	assert.Len(t, drop.Destination.Workloads, 1)
	assert.Equal(t, "Deployment", drop.Destination.Workloads[0].Kind)
	assert.Equal(t, "backend", drop.Destination.Workloads[0].Name)

	// IP
	assert.Equal(t, "10.0.1.5", drop.IP.Source)
	assert.Equal(t, "10.0.2.10", drop.IP.Destination)

	// L4
	assert.Equal(t, ProtocolTCP, drop.L4.Protocol)
	assert.Equal(t, uint32(45678), drop.L4.SourcePort)
	assert.Equal(t, uint32(8080), drop.L4.DestinationPort)
	assert.NotNil(t, drop.L4.TCP)
	assert.True(t, drop.L4.TCP.Flags.SYN)
	assert.False(t, drop.L4.TCP.Flags.ACK)

	// Drop info
	assert.Equal(t, DropReasonPolicy, drop.DropReason)
	assert.Equal(t, "deny-external", drop.PolicyName)
}

func TestFlowDropBuilder_UDP(t *testing.T) {
	drop := NewFlowDropBuilder().
		WithSource("ns", "pod1", nil).
		WithDestination("ns", "pod2", nil).
		WithUDP(12345, 53).
		Build()

	assert.Equal(t, ProtocolUDP, drop.L4.Protocol)
	assert.Equal(t, uint32(12345), drop.L4.SourcePort)
	assert.Equal(t, uint32(53), drop.L4.DestinationPort)
	assert.Nil(t, drop.L4.TCP)
}

func TestParseDropReason(t *testing.T) {
	tests := []struct {
		code     int32
		expected DropReason
	}{
		{130, DropReasonPolicy},
		{133, DropReasonPolicy},
		{134, DropReasonPolicy},
		{131, DropReasonPolicyAuth},
		{181, DropReasonNoMapping},
		{132, DropReasonInvalidPacket},
		{168, DropReasonTTLExceeded},
		{153, DropReasonProxyRedirect},
		{999, DropReasonUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.expected.String(), func(t *testing.T) {
			result := ParseDropReason(tt.code)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseProtocol(t *testing.T) {
	tests := []struct {
		proto    uint8
		expected Protocol
	}{
		{6, ProtocolTCP},
		{17, ProtocolUDP},
		{1, ProtocolICMP},
		{255, ProtocolUnknown},
	}

	for _, tt := range tests {
		t.Run(string(tt.expected), func(t *testing.T) {
			result := ParseProtocol(tt.proto)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDropReason_IsPolicyDrop(t *testing.T) {
	policyDrops := []DropReason{
		DropReasonPolicy,
		DropReasonPolicyL3,
		DropReasonPolicyL4,
		DropReasonPolicyL7,
		DropReasonPolicyAuth,
		DropReasonNoNetworkPolicy,
		DropReasonIngressDenied,
		DropReasonEgressDenied,
	}

	for _, r := range policyDrops {
		assert.True(t, r.IsPolicyDrop(), "expected %s to be a policy drop", r)
	}

	nonPolicyDrops := []DropReason{
		DropReasonInvalidPacket,
		DropReasonTTLExceeded,
		DropReasonNoMapping,
		DropReasonUnknown,
	}

	for _, r := range nonPolicyDrops {
		assert.False(t, r.IsPolicyDrop(), "expected %s to NOT be a policy drop", r)
	}
}

// String implements Stringer for DropReason (for test output)
func (r DropReason) String() string {
	return string(r)
}
