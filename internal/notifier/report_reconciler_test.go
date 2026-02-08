package notifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/nightjarctl/nightjar/internal/types"
)

func TestReportReconciler_BuildReportStatus(t *testing.T) {
	rr := &ReportReconciler{
		logger:             zap.NewNop(),
		remediationBuilder: NewRemediationBuilder("platform@example.com"),
		opts: ReportReconcilerOptions{
			DefaultDetailLevel: types.DetailLevelSummary,
			DefaultContact:     "platform@example.com",
		},
	}

	constraints := []types.Constraint{
		{
			UID:            k8stypes.UID("uid-1"),
			Name:           "critical-netpol",
			Namespace:      "team-alpha",
			ConstraintType: types.ConstraintTypeNetworkEgress,
			Severity:       types.SeverityCritical,
			Effect:         "deny",
			Summary:        "Denies all egress",
			Tags:           []string{"network", "egress"},
			Source:         schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
		},
		{
			UID:            k8stypes.UID("uid-2"),
			Name:           "warning-quota",
			Namespace:      "team-alpha",
			ConstraintType: types.ConstraintTypeResourceLimit,
			Severity:       types.SeverityWarning,
			Effect:         "limit",
			Summary:        "CPU at 85%",
			Tags:           []string{"quota", "cpu"},
			Source:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "resourcequotas"},
			Details: map[string]interface{}{
				"resources": map[string]interface{}{
					"cpu": map[string]interface{}{
						"hard":    "4",
						"used":    "3.4",
						"percent": 85,
					},
				},
			},
		},
		{
			UID:            k8stypes.UID("uid-3"),
			Name:           "info-webhook",
			Namespace:      "",
			ConstraintType: types.ConstraintTypeAdmission,
			Severity:       types.SeverityInfo,
			Effect:         "intercept",
			Summary:        "Webhook validates pods",
			Tags:           []string{"admission"},
			Source:         schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"},
		},
	}

	status := rr.buildReportStatus(constraints, "team-alpha")

	// Check counts
	assert.Equal(t, 3, status.ConstraintCount)
	assert.Equal(t, 1, status.CriticalCount)
	assert.Equal(t, 1, status.WarningCount)
	assert.Equal(t, 1, status.InfoCount)

	// Check human-readable entries
	require.Len(t, status.Constraints, 3)
	// Should be sorted by severity: critical first
	assert.Equal(t, "Critical", status.Constraints[0].Severity)
	assert.Equal(t, "Warning", status.Constraints[1].Severity)
	assert.Equal(t, "Info", status.Constraints[2].Severity)

	// Check machine-readable section
	require.NotNil(t, status.MachineReadable)
	assert.Equal(t, "1", status.MachineReadable.SchemaVersion)
	assert.Equal(t, "summary", status.MachineReadable.DetailLevel)
	require.Len(t, status.MachineReadable.Constraints, 3)

	// Check tags are collected and sorted
	assert.Contains(t, status.MachineReadable.Tags, "network")
	assert.Contains(t, status.MachineReadable.Tags, "quota")
	assert.Contains(t, status.MachineReadable.Tags, "admission")

	// Check resource metrics are extracted
	quotaEntry := status.MachineReadable.Constraints[1] // Warning entry
	require.NotNil(t, quotaEntry.Metrics)
	cpuMetric, ok := quotaEntry.Metrics["cpu"]
	require.True(t, ok)
	assert.Equal(t, "4", cpuMetric.Hard)
	assert.Equal(t, "3.4", cpuMetric.Used)
	assert.Equal(t, float64(85), cpuMetric.PercentUsed)
	assert.Equal(t, "cores", cpuMetric.Unit)
}

func TestReportReconciler_BuildMachineEntry(t *testing.T) {
	rr := &ReportReconciler{
		logger:             zap.NewNop(),
		remediationBuilder: NewRemediationBuilder("platform@example.com"),
		opts: ReportReconcilerOptions{
			DefaultDetailLevel: types.DetailLevelDetailed,
		},
	}

	c := types.Constraint{
		UID:            k8stypes.UID("test-uid"),
		Name:           "test-policy",
		Namespace:      "test-ns",
		ConstraintType: types.ConstraintTypeNetworkIngress,
		Severity:       types.SeverityWarning,
		Effect:         "restrict",
		Summary:        "Restricts ingress",
		Tags:           []string{"network", "ingress"},
		Source:         schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	}

	entry := rr.buildMachineEntry(c, "test-ns")

	assert.Equal(t, "test-uid", entry.UID)
	assert.Equal(t, "test-policy", entry.Name)
	assert.Equal(t, "NetworkIngress", entry.ConstraintType)
	assert.Equal(t, "Warning", entry.Severity)
	assert.Equal(t, "restrict", entry.Effect)

	// Check source ref
	assert.Equal(t, "networking.k8s.io/v1", entry.SourceRef.APIVersion)
	assert.Equal(t, "NetworkPolicy", entry.SourceRef.Kind)
	assert.Equal(t, "test-policy", entry.SourceRef.Name)
	assert.Equal(t, "test-ns", entry.SourceRef.Namespace)

	// Check remediation
	assert.NotEmpty(t, entry.Remediation.Summary)
	assert.NotEmpty(t, entry.Remediation.Steps)

	// Check tags
	assert.Equal(t, []string{"network", "ingress"}, entry.Tags)

	// Check last observed is set
	assert.False(t, entry.LastObserved.IsZero())
}

func TestReportReconciler_ScopedName(t *testing.T) {
	tests := []struct {
		name           string
		detailLevel    types.DetailLevel
		constraintNS   string
		viewerNS       string
		constraintName string
		expectedName   string
	}{
		{
			name:           "summary level, same namespace",
			detailLevel:    types.DetailLevelSummary,
			constraintNS:   "team-alpha",
			viewerNS:       "team-alpha",
			constraintName: "my-policy",
			expectedName:   "my-policy",
		},
		{
			name:           "summary level, cross namespace",
			detailLevel:    types.DetailLevelSummary,
			constraintNS:   "kube-system",
			viewerNS:       "team-alpha",
			constraintName: "cluster-policy",
			expectedName:   "cluster-policy", // Shows "cluster-policy" as redacted name
		},
		{
			name:           "summary level, cluster-scoped",
			detailLevel:    types.DetailLevelSummary,
			constraintNS:   "",
			viewerNS:       "team-alpha",
			constraintName: "global-webhook",
			expectedName:   "global-webhook",
		},
		{
			name:           "detailed level, cross namespace",
			detailLevel:    types.DetailLevelDetailed,
			constraintNS:   "kube-system",
			viewerNS:       "team-alpha",
			constraintName: "detailed-policy",
			expectedName:   "detailed-policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := &ReportReconciler{
				opts: ReportReconcilerOptions{
					DefaultDetailLevel: tt.detailLevel,
				},
			}

			c := types.Constraint{
				Name:      tt.constraintName,
				Namespace: tt.constraintNS,
			}

			result := rr.scopedName(c, tt.viewerNS)
			assert.Equal(t, tt.expectedName, result)
		})
	}
}

func TestSeverityOrder(t *testing.T) {
	assert.Less(t, severityOrder("Critical"), severityOrder("Warning"))
	assert.Less(t, severityOrder("Warning"), severityOrder("Info"))
	assert.Less(t, severityOrder("Info"), severityOrder("Unknown"))
}

func TestGvrToAPIVersion(t *testing.T) {
	tests := []struct {
		gvr      schema.GroupVersionResource
		expected string
	}{
		{
			gvr:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			expected: "v1",
		},
		{
			gvr:      schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
			expected: "networking.k8s.io/v1",
		},
		{
			gvr:      schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
			expected: "apps/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := gvrToAPIVersion(tt.gvr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGvrToKindName(t *testing.T) {
	tests := []struct {
		resource string
		expected string
	}{
		{"networkpolicies", "NetworkPolicy"},
		{"resourcequotas", "ResourceQuota"},
		{"limitranges", "LimitRange"},
		{"validatingwebhookconfigurations", "ValidatingWebhookConfiguration"},
		{"mutatingwebhookconfigurations", "MutatingWebhookConfiguration"},
		{"ciliumnetworkpolicies", "CiliumNetworkPolicy"},
		{"pods", "Pod"},
		{"deployments", "Deployment"},
		{"statefulsets", "StatefulSet"},
		{"customresources", "Customresource"}, // Generic handling
	}

	for _, tt := range tests {
		t.Run(tt.resource, func(t *testing.T) {
			gvr := schema.GroupVersionResource{Resource: tt.resource}
			result := gvrToKindName(gvr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultReportReconcilerOptions(t *testing.T) {
	opts := DefaultReportReconcilerOptions()

	assert.Equal(t, 10*1000*1000*1000, int(opts.DebounceDuration)) // 10 seconds
	assert.Equal(t, types.DetailLevelSummary, opts.DefaultDetailLevel)
	assert.Equal(t, "your platform team", opts.DefaultContact)
}

func TestReportReconciler_ExtractResourceMetrics(t *testing.T) {
	rr := &ReportReconciler{}

	c := types.Constraint{
		Details: map[string]interface{}{
			"resources": map[string]interface{}{
				"cpu": map[string]interface{}{
					"hard":    "4",
					"used":    "3.2",
					"percent": 80,
				},
				"memory": map[string]interface{}{
					"hard":    "8Gi",
					"used":    "6Gi",
					"percent": 75.5,
				},
				"pods": map[string]interface{}{
					"hard":    "100",
					"used":    "50",
					"percent": 50,
				},
			},
		},
	}

	metrics := rr.extractResourceMetrics(c)

	require.NotNil(t, metrics)
	require.Len(t, metrics, 3)

	cpu := metrics["cpu"]
	assert.Equal(t, "4", cpu.Hard)
	assert.Equal(t, "3.2", cpu.Used)
	assert.Equal(t, float64(80), cpu.PercentUsed)
	assert.Equal(t, "cores", cpu.Unit)

	memory := metrics["memory"]
	assert.Equal(t, "8Gi", memory.Hard)
	assert.Equal(t, "6Gi", memory.Used)
	assert.Equal(t, 75.5, memory.PercentUsed)
	assert.Equal(t, "bytes", memory.Unit)

	pods := metrics["pods"]
	assert.Equal(t, "100", pods.Hard)
	assert.Equal(t, "50", pods.Used)
	assert.Equal(t, float64(50), pods.PercentUsed)
	assert.Equal(t, "count", pods.Unit)
}

func TestReportReconciler_ExtractResourceMetrics_NilDetails(t *testing.T) {
	rr := &ReportReconciler{}

	c := types.Constraint{
		Details: nil,
	}

	metrics := rr.extractResourceMetrics(c)
	assert.Nil(t, metrics)
}

func TestReportReconciler_ExtractResourceMetrics_NoResources(t *testing.T) {
	rr := &ReportReconciler{}

	c := types.Constraint{
		Details: map[string]interface{}{
			"other": "data",
		},
	}

	metrics := rr.extractResourceMetrics(c)
	assert.Nil(t, metrics)
}
