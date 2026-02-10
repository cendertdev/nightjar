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

func TestParse_ViaInterface(t *testing.T) {
	a := New()

	// Create an object with GVK set (as it would be when using the Parse interface method)
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "custom.example.com/v1beta1",
			"kind":       "SecurityPolicy",
			"metadata": map[string]interface{}{
				"name":      "my-sec-policy",
				"namespace": "team-gamma",
				"uid":       "uid-parse-via-interface",
			},
			"spec": map[string]interface{}{
				"podSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"role": "db",
					},
				},
			},
		},
	}
	// Set GVK explicitly (normally done by the API server or informer)
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "custom.example.com",
		Version: "v1beta1",
		Kind:    "SecurityPolicy",
	})

	constraints, err := a.Parse(context.Background(), obj)
	require.NoError(t, err)
	require.Len(t, constraints, 1)

	c := constraints[0]
	// Parse should have guessed the resource from the kind
	assert.Equal(t, "custom.example.com", c.Source.Group)
	assert.Equal(t, "v1beta1", c.Source.Version)
	assert.Equal(t, "securitypolicies", c.Source.Resource)
	assert.Equal(t, "my-sec-policy", c.Name)
	assert.Equal(t, "team-gamma", c.Namespace)
	assert.Equal(t, types.ConstraintTypeUnknown, c.ConstraintType)
	assert.Equal(t, types.SeverityInfo, c.Severity)

	// Verify summary was auto-generated (not from annotation)
	assert.Contains(t, c.Summary, "SecurityPolicy")
	assert.Contains(t, c.Summary, "my-sec-policy")
	assert.Contains(t, c.Summary, "team-gamma")
}

func TestParseWithGVR_ClusterScoped(t *testing.T) {
	a := New()

	// Cluster-scoped object has no namespace
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "security.io/v1",
			"kind":       "ClusterConstraint",
			"metadata": map[string]interface{}{
				"name": "global-deny",
				"uid":  "uid-cluster-scoped",
			},
			"spec": map[string]interface{}{
				"rules": []interface{}{
					map[string]interface{}{
						"action": "deny",
					},
				},
			},
		},
	}

	gvr := schema.GroupVersionResource{Group: "security.io", Version: "v1", Resource: "clusterconstraints"}

	constraints, err := a.ParseWithGVR(context.Background(), obj, gvr)
	require.NoError(t, err)
	require.Len(t, constraints, 1)

	c := constraints[0]
	assert.Equal(t, "global-deny", c.Name)
	assert.Empty(t, c.Namespace)
	assert.Empty(t, c.AffectedNamespaces, "cluster-scoped objects should have no affected namespaces")

	// Summary should say "cluster-scoped"
	assert.Contains(t, c.Summary, "cluster-scoped")

	// Rules should be discovered
	assert.Contains(t, c.Details["discoveredFields"], "rules")
	assert.Equal(t, 1, c.Details["ruleCount"])
}

func TestParseWithGVR_AllAnnotations(t *testing.T) {
	a := New()

	tests := []struct {
		name             string
		severity         string
		expectedSeverity types.Severity
	}{
		{"critical-lower", "critical", types.SeverityCritical},
		{"critical-upper", "Critical", types.SeverityCritical},
		{"warning-lower", "warning", types.SeverityWarning},
		{"warning-upper", "Warning", types.SeverityWarning},
		{"info-lower", "info", types.SeverityInfo},
		{"info-upper", "Info", types.SeverityInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestPolicy",
					"metadata": map[string]interface{}{
						"name":      "annotated-" + tt.name,
						"namespace": "ns",
						"annotations": map[string]interface{}{
							"nightjar.io/severity":        tt.severity,
							"nightjar.io/summary":         "Custom summary for " + tt.name,
							"nightjar.io/constraint-type": "Admission",
						},
					},
					"spec": map[string]interface{}{},
				},
			}

			gvr := schema.GroupVersionResource{Group: "test.io", Version: "v1", Resource: "testpolicies"}
			constraints, err := a.ParseWithGVR(context.Background(), obj, gvr)
			require.NoError(t, err)
			require.Len(t, constraints, 1)

			c := constraints[0]
			assert.Equal(t, tt.expectedSeverity, c.Severity)
			assert.Equal(t, "Custom summary for "+tt.name, c.Summary)
			assert.Equal(t, types.ConstraintTypeAdmission, c.ConstraintType)
		})
	}
}

func TestParseWithGVR_AllConstraintTypes(t *testing.T) {
	a := New()

	tests := []struct {
		typeAnnotation string
		expectedType   types.ConstraintType
	}{
		{"NetworkIngress", types.ConstraintTypeNetworkIngress},
		{"NetworkEgress", types.ConstraintTypeNetworkEgress},
		{"Admission", types.ConstraintTypeAdmission},
		{"ResourceLimit", types.ConstraintTypeResourceLimit},
		{"MeshPolicy", types.ConstraintTypeMeshPolicy},
		{"MissingResource", types.ConstraintTypeMissing},
	}

	for _, tt := range tests {
		t.Run(tt.typeAnnotation, func(t *testing.T) {
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestPolicy",
					"metadata": map[string]interface{}{
						"name":      "type-test-" + tt.typeAnnotation,
						"namespace": "ns",
						"annotations": map[string]interface{}{
							"nightjar.io/constraint-type": tt.typeAnnotation,
						},
					},
					"spec": map[string]interface{}{},
				},
			}

			gvr := schema.GroupVersionResource{Group: "test.io", Version: "v1", Resource: "testpolicies"}
			constraints, err := a.ParseWithGVR(context.Background(), obj, gvr)
			require.NoError(t, err)
			require.Len(t, constraints, 1)

			assert.Equal(t, tt.expectedType, constraints[0].ConstraintType)
		})
	}
}
