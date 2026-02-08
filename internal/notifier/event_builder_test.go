package notifier

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/nightjarctl/nightjar/internal/annotations"
	"github.com/nightjarctl/nightjar/internal/types"
)

func TestEventBuilder_BuildEvent(t *testing.T) {
	eb := NewEventBuilder("platform-team@example.com")

	c := types.Constraint{
		UID:       k8stypes.UID("constraint-123"),
		Name:      "restrict-egress",
		Namespace: "team-alpha",
		Source: schema.GroupVersionResource{
			Group:    "networking.k8s.io",
			Version:  "v1",
			Resource: "networkpolicies",
		},
		ConstraintType:  types.ConstraintTypeNetworkEgress,
		Effect:          "restrict",
		Severity:        types.SeverityWarning,
		Summary:         "NetworkPolicy restricts egress to ports 443, 8443",
		RemediationHint: "Contact platform-team@example.com for exceptions",
		Tags:            []string{"network", "egress"},
	}

	workload := WorkloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "api-server",
		Namespace:  "team-alpha",
		UID:        "workload-456",
	}

	event := eb.BuildEvent(c, types.DetailLevelDetailed, workload, "Egress restricted")

	// Check basic event properties
	assert.Equal(t, "team-alpha", event.Namespace)
	assert.Equal(t, "ConstraintNotification", event.Reason)
	assert.Equal(t, "Egress restricted", event.Message)
	assert.Equal(t, corev1.EventTypeWarning, event.Type)
	assert.Equal(t, "nightjar-controller", event.Source.Component)

	// Check involved object
	assert.Equal(t, "apps/v1", event.InvolvedObject.APIVersion)
	assert.Equal(t, "Deployment", event.InvolvedObject.Kind)
	assert.Equal(t, "api-server", event.InvolvedObject.Name)
	assert.Equal(t, "team-alpha", event.InvolvedObject.Namespace)

	// Check labels
	assert.Equal(t, annotations.ManagedByValue, event.Labels[annotations.LabelManagedBy])
	assert.Equal(t, "warning", event.Labels[annotations.LabelSeverity])
	assert.Equal(t, "network-egress", event.Labels[annotations.LabelConstraintType])

	// Check annotations
	assert.Equal(t, annotations.ManagedByValue, event.Annotations[annotations.ManagedBy])
	assert.Equal(t, "NetworkEgress", event.Annotations[annotations.EventConstraintType])
	assert.Equal(t, "Warning", event.Annotations[annotations.EventSeverity])
	assert.Equal(t, "restrict", event.Annotations[annotations.EventEffect])
	assert.Equal(t, "detailed", event.Annotations[annotations.EventDetailLevel])
	assert.Equal(t, "restrict-egress", event.Annotations[annotations.EventConstraintName])
	assert.Equal(t, "team-alpha", event.Annotations[annotations.EventConstraintNamespace])
}

func TestEventBuilder_StructuredData(t *testing.T) {
	eb := NewEventBuilder("platform@example.com")

	c := types.Constraint{
		UID:       k8stypes.UID("uid-789"),
		Name:      "quota-limit",
		Namespace: "team-beta",
		Source: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "resourcequotas",
		},
		ConstraintType: types.ConstraintTypeResourceLimit,
		Effect:         "limit",
		Severity:       types.SeverityCritical,
		Summary:        "CPU at 95% of quota",
		Tags:           []string{"quota", "cpu"},
		Details: map[string]interface{}{
			"resources": map[string]interface{}{
				"cpu": map[string]interface{}{
					"hard":    "4",
					"used":    "3.8",
					"percent": 95,
				},
			},
		},
	}

	workload := WorkloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "worker",
		Namespace:  "team-beta",
		UID:        "workload-abc",
	}

	event := eb.BuildEvent(c, types.DetailLevelFull, workload, "Quota critical")

	// Parse structured data
	jsonStr := event.Annotations[annotations.EventStructuredData]
	require.NotEmpty(t, jsonStr, "Should have structured data annotation")

	var data EventStructuredData
	err := json.Unmarshal([]byte(jsonStr), &data)
	require.NoError(t, err)

	assert.Equal(t, "1", data.SchemaVersion)
	assert.Equal(t, "uid-789", data.ConstraintUID)
	assert.Equal(t, "quota-limit", data.ConstraintName)
	assert.Equal(t, "team-beta", data.ConstraintNamespace)
	assert.Equal(t, "ResourceLimit", data.ConstraintType)
	assert.Equal(t, "Critical", data.Severity)
	assert.Equal(t, "limit", data.Effect)
	assert.Equal(t, "core/v1/resourcequotas", data.SourceGVR)
	assert.Equal(t, "Deployment", data.WorkloadKind)
	assert.Equal(t, "worker", data.WorkloadName)
	assert.Equal(t, "team-beta", data.WorkloadNamespace)
	assert.Contains(t, data.Tags, "quota")
	assert.Contains(t, data.Tags, "cpu")
	assert.Equal(t, "full", data.DetailLevel)
	assert.NotEmpty(t, data.ObservedAt)

	// Check metrics
	require.NotNil(t, data.Metrics)
	cpuMetric, ok := data.Metrics["cpu"]
	require.True(t, ok)
	assert.Equal(t, "4", cpuMetric.Hard)
	assert.Equal(t, "3.8", cpuMetric.Used)
	assert.Equal(t, float64(95), cpuMetric.PercentUsed)
	assert.Equal(t, "cores", cpuMetric.Unit)

	// Check remediation
	require.NotNil(t, data.Remediation)
	assert.NotEmpty(t, data.Remediation.Summary)
	assert.NotEmpty(t, data.Remediation.Steps)
}

func TestEventBuilder_PrivacyScoping_Summary(t *testing.T) {
	eb := NewEventBuilder("platform@example.com")

	// Cross-namespace constraint
	c := types.Constraint{
		UID:       k8stypes.UID("cross-ns-uid"),
		Name:      "cluster-egress-policy",
		Namespace: "kube-system",
		Source: schema.GroupVersionResource{
			Group:    "cilium.io",
			Version:  "v2",
			Resource: "ciliumclusterwidenetworkpolicies",
		},
		ConstraintType: types.ConstraintTypeNetworkEgress,
		Effect:         "deny",
		Severity:       types.SeverityCritical,
		Summary:        "Cluster-wide egress policy blocks port 9090 to monitoring namespace",
	}

	workload := WorkloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "api-server",
		Namespace:  "team-alpha", // Different from constraint namespace
		UID:        "workload-123",
	}

	event := eb.BuildEvent(c, types.DetailLevelSummary, workload, "Traffic blocked")

	// At summary level, cross-namespace constraint name should be redacted
	assert.Equal(t, "redacted", event.Annotations[annotations.EventConstraintName])

	// Namespace should NOT be in annotations
	_, hasNs := event.Annotations[annotations.EventConstraintNamespace]
	assert.False(t, hasNs, "Should not expose cross-namespace constraint namespace at summary level")

	// Check structured data also respects privacy
	var data EventStructuredData
	err := json.Unmarshal([]byte(event.Annotations[annotations.EventStructuredData]), &data)
	require.NoError(t, err)

	assert.Equal(t, "redacted", data.ConstraintName)
	assert.Empty(t, data.ConstraintNamespace)
	assert.Equal(t, "summary", data.DetailLevel)

	// Summary should be generic, not specific
	assert.NotContains(t, data.Summary, "9090")
	assert.NotContains(t, data.Summary, "monitoring")
}

func TestEventBuilder_PrivacyScoping_SameNamespace(t *testing.T) {
	eb := NewEventBuilder("platform@example.com")

	// Same-namespace constraint
	c := types.Constraint{
		UID:       k8stypes.UID("same-ns-uid"),
		Name:      "team-policy",
		Namespace: "team-alpha",
		Source: schema.GroupVersionResource{
			Group:    "networking.k8s.io",
			Version:  "v1",
			Resource: "networkpolicies",
		},
		ConstraintType: types.ConstraintTypeNetworkIngress,
		Effect:         "restrict",
		Severity:       types.SeverityWarning,
		Summary:        "Policy restricts ingress",
	}

	workload := WorkloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "web-server",
		Namespace:  "team-alpha", // Same as constraint namespace
		UID:        "workload-456",
	}

	event := eb.BuildEvent(c, types.DetailLevelSummary, workload, "Ingress restricted")

	// Same-namespace constraint name should be shown even at summary level
	assert.Equal(t, "team-policy", event.Annotations[annotations.EventConstraintName])
	assert.Equal(t, "team-alpha", event.Annotations[annotations.EventConstraintNamespace])
}

func TestEventBuilder_EventType(t *testing.T) {
	eb := NewEventBuilder("platform@example.com")

	workload := WorkloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "test",
		Namespace:  "test-ns",
	}

	tests := []struct {
		severity     types.Severity
		expectedType string
	}{
		{types.SeverityCritical, corev1.EventTypeWarning},
		{types.SeverityWarning, corev1.EventTypeWarning},
		{types.SeverityInfo, corev1.EventTypeNormal},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			c := types.Constraint{
				UID:            k8stypes.UID("test-uid"),
				Name:           "test",
				ConstraintType: types.ConstraintTypeAdmission,
				Severity:       tt.severity,
			}

			event := eb.BuildEvent(c, types.DetailLevelSummary, workload, "Test")
			assert.Equal(t, tt.expectedType, event.Type)
		})
	}
}

func TestFormatGVR(t *testing.T) {
	tests := []struct {
		gvr      schema.GroupVersionResource
		expected string
	}{
		{
			gvr:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			expected: "core/v1/pods",
		},
		{
			gvr:      schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
			expected: "networking.k8s.io/v1/networkpolicies",
		},
		{
			gvr:      schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"},
			expected: "cilium.io/v2/ciliumnetworkpolicies",
		},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatGVR(tt.gvr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToKebabCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"NetworkIngress", "network-ingress"},
		{"NetworkEgress", "network-egress"},
		{"ResourceLimit", "resource-limit"},
		{"MeshPolicy", "mesh-policy"},
		{"MissingResource", "missing-resource"},
		{"Admission", "admission"},
		{"Unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := toKebabCase(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGuessUnit(t *testing.T) {
	tests := []struct {
		resourceName string
		expected     string
	}{
		{"cpu", "cores"},
		{"limits.cpu", "cores"},
		{"memory", "bytes"},
		{"requests.memory", "bytes"},
		{"pods", "count"},
		{"services", "count"},
		{"requests.storage", "bytes"},
		{"persistentvolumeclaims", "count"},
	}

	for _, tt := range tests {
		t.Run(tt.resourceName, func(t *testing.T) {
			result := guessUnit(tt.resourceName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEventBuilder_RemediationInAnnotations(t *testing.T) {
	eb := NewEventBuilder("security-team@example.com")

	c := types.Constraint{
		UID:       k8stypes.UID("webhook-uid"),
		Name:      "pod-security",
		Namespace: "",
		Source: schema.GroupVersionResource{
			Group:    "admissionregistration.k8s.io",
			Version:  "v1",
			Resource: "validatingwebhookconfigurations",
		},
		ConstraintType: types.ConstraintTypeAdmission,
		Effect:         "intercept",
		Severity:       types.SeverityWarning,
	}

	workload := WorkloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "my-app",
		Namespace:  "default",
	}

	event := eb.BuildEvent(c, types.DetailLevelDetailed, workload, "Webhook check")

	// Should have remediation type
	assert.Equal(t, "kubectl", event.Annotations[annotations.EventRemediationType])
}
