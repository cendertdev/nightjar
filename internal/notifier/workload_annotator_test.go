package notifier

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/nightjarctl/nightjar/internal/annotations"
	"github.com/nightjarctl/nightjar/internal/types"
)

func TestWorkloadAnnotator_BuildAnnotationPatch_Empty(t *testing.T) {
	wa := &WorkloadAnnotator{}

	patch := wa.buildAnnotationPatch(nil)

	annots := patch["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})

	// All annotations should be nil (removed)
	assert.Nil(t, annots[annotations.WorkloadStatus])
	assert.Nil(t, annots[annotations.WorkloadConstraints])
	assert.Nil(t, annots[annotations.WorkloadMaxSeverity])
	assert.Nil(t, annots[annotations.WorkloadCriticalCount])
	assert.Nil(t, annots[annotations.WorkloadWarningCount])
	assert.Nil(t, annots[annotations.WorkloadInfoCount])
}

func TestWorkloadAnnotator_BuildAnnotationPatch_WithConstraints(t *testing.T) {
	wa := &WorkloadAnnotator{}

	constraints := []types.Constraint{
		{
			UID:            k8stypes.UID("uid-1"),
			Name:           "critical-policy",
			ConstraintType: types.ConstraintTypeNetworkEgress,
			Severity:       types.SeverityCritical,
			Source:         schema.GroupVersionResource{Resource: "networkpolicies"},
		},
		{
			UID:            k8stypes.UID("uid-2"),
			Name:           "warning-policy",
			ConstraintType: types.ConstraintTypeAdmission,
			Severity:       types.SeverityWarning,
			Source:         schema.GroupVersionResource{Resource: "validatingwebhookconfigurations"},
		},
		{
			UID:            k8stypes.UID("uid-3"),
			Name:           "info-policy",
			ConstraintType: types.ConstraintTypeResourceLimit,
			Severity:       types.SeverityInfo,
			Source:         schema.GroupVersionResource{Resource: "resourcequotas"},
		},
	}

	patch := wa.buildAnnotationPatch(constraints)

	annots := patch["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})

	// Check status string
	status := annots[annotations.WorkloadStatus].(string)
	assert.Contains(t, status, "3 constraints")
	assert.Contains(t, status, "1 critical")
	assert.Contains(t, status, "1 warning")

	// Check counts
	assert.Equal(t, "1", annots[annotations.WorkloadCriticalCount])
	assert.Equal(t, "1", annots[annotations.WorkloadWarningCount])
	assert.Equal(t, "1", annots[annotations.WorkloadInfoCount])

	// Check max severity
	assert.Equal(t, "critical", annots[annotations.WorkloadMaxSeverity])

	// Check constraints JSON
	constraintsJSON := annots[annotations.WorkloadConstraints].(string)
	var summaries []ConstraintSummary
	err := json.Unmarshal([]byte(constraintsJSON), &summaries)
	require.NoError(t, err)
	require.Len(t, summaries, 3)

	assert.Equal(t, "NetworkEgress", summaries[0].Type)
	assert.Equal(t, "Critical", summaries[0].Severity)
	assert.Equal(t, "critical-policy", summaries[0].Name)
	assert.Equal(t, "networkpolicies", summaries[0].Source)

	// Check last evaluated is set
	lastEvaluated := annots[annotations.WorkloadLastEvaluated].(string)
	assert.NotEmpty(t, lastEvaluated)
}

func TestWorkloadAnnotator_BuildAnnotationPatch_OnlyInfo(t *testing.T) {
	wa := &WorkloadAnnotator{}

	constraints := []types.Constraint{
		{
			UID:            k8stypes.UID("uid-1"),
			Name:           "info-only",
			ConstraintType: types.ConstraintTypeResourceLimit,
			Severity:       types.SeverityInfo,
			Source:         schema.GroupVersionResource{Resource: "resourcequotas"},
		},
	}

	patch := wa.buildAnnotationPatch(constraints)
	annots := patch["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})

	assert.Equal(t, "info", annots[annotations.WorkloadMaxSeverity])
	assert.Equal(t, "0", annots[annotations.WorkloadCriticalCount])
	assert.Equal(t, "0", annots[annotations.WorkloadWarningCount])
	assert.Equal(t, "1", annots[annotations.WorkloadInfoCount])

	status := annots[annotations.WorkloadStatus].(string)
	assert.Equal(t, "1 constraints", status)
}

func TestWorkloadAnnotator_BuildAnnotationPatch_OnlyWarning(t *testing.T) {
	wa := &WorkloadAnnotator{}

	constraints := []types.Constraint{
		{
			UID:            k8stypes.UID("uid-1"),
			Name:           "warning-1",
			ConstraintType: types.ConstraintTypeAdmission,
			Severity:       types.SeverityWarning,
			Source:         schema.GroupVersionResource{Resource: "validatingwebhookconfigurations"},
		},
		{
			UID:            k8stypes.UID("uid-2"),
			Name:           "warning-2",
			ConstraintType: types.ConstraintTypeNetworkIngress,
			Severity:       types.SeverityWarning,
			Source:         schema.GroupVersionResource{Resource: "networkpolicies"},
		},
	}

	patch := wa.buildAnnotationPatch(constraints)
	annots := patch["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})

	assert.Equal(t, "warning", annots[annotations.WorkloadMaxSeverity])
	assert.Equal(t, "0", annots[annotations.WorkloadCriticalCount])
	assert.Equal(t, "2", annots[annotations.WorkloadWarningCount])
	assert.Equal(t, "0", annots[annotations.WorkloadInfoCount])

	status := annots[annotations.WorkloadStatus].(string)
	assert.Contains(t, status, "2 constraints")
	assert.Contains(t, status, "2 warning")
}

func TestWorkloadAnnotator_BuildStatusString(t *testing.T) {
	wa := &WorkloadAnnotator{}

	tests := []struct {
		name     string
		total    int
		critical int
		warning  int
		expected string
	}{
		{
			name:     "no constraints",
			total:    0,
			critical: 0,
			warning:  0,
			expected: "No constraints",
		},
		{
			name:     "only critical",
			total:    2,
			critical: 2,
			warning:  0,
			expected: "2 constraints (2 critical)",
		},
		{
			name:     "only warning",
			total:    3,
			critical: 0,
			warning:  3,
			expected: "3 constraints (3 warning)",
		},
		{
			name:     "critical and warning",
			total:    5,
			critical: 2,
			warning:  3,
			expected: "5 constraints (2 critical, 3 warning)",
		},
		{
			name:     "only info (no critical or warning)",
			total:    4,
			critical: 0,
			warning:  0,
			expected: "4 constraints",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wa.buildStatusString(tt.total, tt.critical, tt.warning)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestKindToGVR(t *testing.T) {
	tests := []struct {
		kind        string
		expectError bool
		expectedGVR schema.GroupVersionResource
	}{
		{
			kind: "Deployment",
			expectedGVR: schema.GroupVersionResource{
				Group: "apps", Version: "v1", Resource: "deployments",
			},
		},
		{
			kind: "StatefulSet",
			expectedGVR: schema.GroupVersionResource{
				Group: "apps", Version: "v1", Resource: "statefulsets",
			},
		},
		{
			kind: "DaemonSet",
			expectedGVR: schema.GroupVersionResource{
				Group: "apps", Version: "v1", Resource: "daemonsets",
			},
		},
		{
			kind: "ReplicaSet",
			expectedGVR: schema.GroupVersionResource{
				Group: "apps", Version: "v1", Resource: "replicasets",
			},
		},
		{
			kind: "Job",
			expectedGVR: schema.GroupVersionResource{
				Group: "batch", Version: "v1", Resource: "jobs",
			},
		},
		{
			kind: "CronJob",
			expectedGVR: schema.GroupVersionResource{
				Group: "batch", Version: "v1", Resource: "cronjobs",
			},
		},
		{
			kind: "Pod",
			expectedGVR: schema.GroupVersionResource{
				Group: "", Version: "v1", Resource: "pods",
			},
		},
		{
			kind:        "Unknown",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			gvr, err := kindToGVR(tt.kind)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedGVR, gvr)
			}
		})
	}
}

func TestJoinWithComma(t *testing.T) {
	tests := []struct {
		parts    []string
		expected string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"one"}, "one"},
		{[]string{"one", "two"}, "one, two"},
		{[]string{"one", "two", "three"}, "one, two, three"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := joinWithComma(tt.parts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultWorkloadAnnotatorOptions(t *testing.T) {
	opts := DefaultWorkloadAnnotatorOptions()

	assert.Equal(t, 30*1000*1000*1000, int(opts.DebounceDuration)) // 30 seconds in nanoseconds
	assert.Equal(t, 5, opts.Workers)
}
