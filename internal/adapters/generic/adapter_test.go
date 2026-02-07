package generic

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/nightjarctl/nightjar/internal/types"
)

func loadFixture(t *testing.T, path string) *unstructured.Unstructured {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	obj := &unstructured.Unstructured{}
	require.NoError(t, yaml.Unmarshal(data, &obj.Object))
	return obj
}

func TestName(t *testing.T) {
	a := New()
	assert.Equal(t, "generic", a.Name())
}

func TestHandles(t *testing.T) {
	a := New()
	gvrs := a.Handles()
	assert.Nil(t, gvrs, "generic adapter should not register specific GVRs")
}

func TestParse_UnknownCRD(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/unknown_crd.yaml")
	gvr := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "custompolicies"}

	constraints, err := a.ParseWithGVR(context.Background(), obj, gvr)
	require.NoError(t, err)
	require.Len(t, constraints, 1)

	c := constraints[0]
	assert.Equal(t, types.ConstraintTypeUnknown, c.ConstraintType)
	assert.Equal(t, types.SeverityInfo, c.Severity)
	assert.Equal(t, "test-policy", c.Name)
	assert.Equal(t, "team-alpha", c.Namespace)
	assert.Contains(t, c.Summary, "CustomPolicy")
	assert.Contains(t, c.Summary, "test-policy")

	// Check that workload selector was discovered
	require.NotNil(t, c.WorkloadSelector)
	assert.Equal(t, "web", c.WorkloadSelector.MatchLabels["app"])

	// Check details
	assert.Contains(t, c.Details["discoveredFields"], "workloadSelector")
	assert.Contains(t, c.Details["discoveredFields"], "rules")
}

func TestParse_AnnotatedCRD(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/annotated_crd.yaml")
	gvr := schema.GroupVersionResource{Group: "custom.io", Version: "v1", Resource: "policyrules"}

	constraints, err := a.ParseWithGVR(context.Background(), obj, gvr)
	require.NoError(t, err)
	require.Len(t, constraints, 1)

	c := constraints[0]
	assert.Equal(t, types.ConstraintTypeAdmission, c.ConstraintType, "type should come from annotation")
	assert.Equal(t, types.SeverityWarning, c.Severity, "severity should come from annotation")
	assert.Equal(t, "Custom policy blocks unapproved images", c.Summary, "summary should come from annotation")

	// Check that parameters were discovered
	assert.True(t, c.Details["hasParameters"].(bool))
	assert.Contains(t, c.Details["discoveredFields"], "parameters")
}

func TestParse_DoesNotMutateInput(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/unknown_crd.yaml")
	before := obj.DeepCopy()
	gvr := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "custompolicies"}

	_, err := a.ParseWithGVR(context.Background(), obj, gvr)
	require.NoError(t, err)
	assert.Equal(t, before.Object, obj.Object, "Parse() must not mutate the input object")
}

func TestGuessResource(t *testing.T) {
	tests := []struct {
		kind     string
		expected string
	}{
		{"Pod", "pods"},
		{"Policy", "policies"},
		{"Ingress", "ingresses"},
		{"NetworkPolicy", "networkpolicies"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			assert.Equal(t, tt.expected, guessResource(tt.kind))
		})
	}
}
