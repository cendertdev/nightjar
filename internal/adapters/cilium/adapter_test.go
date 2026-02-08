package cilium

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/nightjarctl/nightjar/internal/types"
)

func loadFixture(t *testing.T, path string) *unstructured.Unstructured {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	obj := &unstructured.Unstructured{}
	err = yaml.Unmarshal(data, &obj.Object)
	require.NoError(t, err)

	return obj
}

func TestAdapter_Name(t *testing.T) {
	a := New()
	assert.Equal(t, "cilium", a.Name())
}

func TestAdapter_Handles(t *testing.T) {
	a := New()
	gvrs := a.Handles()
	require.Len(t, gvrs, 2)

	assert.Equal(t, "cilium.io", gvrs[0].Group)
	assert.Equal(t, "v2", gvrs[0].Version)
	assert.Equal(t, "ciliumnetworkpolicies", gvrs[0].Resource)

	assert.Equal(t, "cilium.io", gvrs[1].Group)
	assert.Equal(t, "v2", gvrs[1].Version)
	assert.Equal(t, "ciliumclusterwidenetworkpolicies", gvrs[1].Resource)
}

func TestParse_BasicIngress(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/basic_ingress.yaml")

	constraints, err := a.Parse(context.Background(), obj)
	require.NoError(t, err)
	require.Len(t, constraints, 1)

	c := constraints[0]
	assert.Equal(t, "allow-frontend", c.Name)
	assert.Equal(t, "production", c.Namespace)
	assert.Equal(t, types.ConstraintTypeNetworkIngress, c.ConstraintType)
	assert.Equal(t, types.SeverityWarning, c.Severity)
	assert.Equal(t, "restrict", c.Effect)
	assert.Contains(t, c.Summary, "restricts ingress")

	// Check workload selector
	require.NotNil(t, c.WorkloadSelector)
	assert.Equal(t, "backend", c.WorkloadSelector.MatchLabels["app"])

	// Check details
	require.NotNil(t, c.Details)
	assert.Equal(t, 1, c.Details["allowRuleCount"])
	ports, ok := c.Details["ports"].([]string)
	require.True(t, ok)
	assert.Contains(t, ports, "8080/TCP")
}

func TestParse_EgressFQDN(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/egress_fqdn.yaml")

	constraints, err := a.Parse(context.Background(), obj)
	require.NoError(t, err)
	require.Len(t, constraints, 1)

	c := constraints[0]
	assert.Equal(t, "allow-external-api", c.Name)
	assert.Equal(t, types.ConstraintTypeNetworkEgress, c.ConstraintType)
	assert.Equal(t, types.SeverityWarning, c.Severity)
	assert.Contains(t, c.Summary, "api.example.com")
	assert.Contains(t, c.Summary, "googleapis.com")

	// Check details
	require.NotNil(t, c.Details)
	fqdns, ok := c.Details["fqdns"].([]string)
	require.True(t, ok)
	assert.Contains(t, fqdns, "api.example.com")
	assert.Contains(t, fqdns, "*.googleapis.com")
}

func TestParse_DenyRules(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/deny_rules.yaml")

	constraints, err := a.Parse(context.Background(), obj)
	require.NoError(t, err)
	require.Len(t, constraints, 2)

	// Ingress deny
	var ingressC, egressC types.Constraint
	for _, c := range constraints {
		if c.ConstraintType == types.ConstraintTypeNetworkIngress {
			ingressC = c
		} else {
			egressC = c
		}
	}

	assert.Equal(t, types.SeverityCritical, ingressC.Severity)
	assert.Equal(t, "deny", ingressC.Effect)
	assert.Contains(t, ingressC.Summary, "explicitly denies")

	assert.Equal(t, types.SeverityCritical, egressC.Severity)
	assert.Equal(t, "deny", egressC.Effect)
	assert.Contains(t, egressC.Summary, "explicitly denies")
}

func TestParse_L7HTTP(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/l7_http.yaml")

	constraints, err := a.Parse(context.Background(), obj)
	require.NoError(t, err)
	require.Len(t, constraints, 1)

	c := constraints[0]
	assert.Contains(t, c.Summary, "L7")

	// Check L7 types in details
	require.NotNil(t, c.Details)
	l7Types, ok := c.Details["l7Types"].([]string)
	require.True(t, ok)
	assert.Contains(t, l7Types, "http")
}

func TestParse_ClusterWide(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/clusterwide.yaml")

	constraints, err := a.Parse(context.Background(), obj)
	require.NoError(t, err)
	require.Len(t, constraints, 1)

	c := constraints[0]
	assert.Equal(t, "default-deny-external", c.Name)
	assert.Empty(t, c.Namespace) // Cluster-wide has no namespace
	assert.Empty(t, c.AffectedNamespaces)
	assert.Equal(t, types.ConstraintTypeNetworkEgress, c.ConstraintType)
	assert.Equal(t, types.SeverityCritical, c.Severity)
	assert.Contains(t, c.Summary, "CiliumClusterwideNetworkPolicy")
	assert.Contains(t, c.RemediationHint, "CiliumClusterwideNetworkPolicy")

	// Check source GVR
	assert.Equal(t, "ciliumclusterwidenetworkpolicies", c.Source.Resource)
}

func TestParse_Entities(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/entities.yaml")

	constraints, err := a.Parse(context.Background(), obj)
	require.NoError(t, err)
	require.Len(t, constraints, 2)

	// Find ingress constraint
	var ingressC types.Constraint
	for _, c := range constraints {
		if c.ConstraintType == types.ConstraintTypeNetworkIngress {
			ingressC = c
			break
		}
	}

	require.NotNil(t, ingressC.Details)
	entities, ok := ingressC.Details["entities"].([]string)
	require.True(t, ok)
	assert.Contains(t, entities, "cluster")
	assert.Contains(t, entities, "host")
	assert.Contains(t, ingressC.Summary, "cluster")
}

func TestParse_DenyAll(t *testing.T) {
	a := New()
	obj := loadFixture(t, "testdata/deny_all.yaml")

	constraints, err := a.Parse(context.Background(), obj)
	require.NoError(t, err)
	require.Len(t, constraints, 1)

	c := constraints[0]
	assert.Equal(t, "deny-all", c.Name)
	assert.Equal(t, types.ConstraintTypeNetworkIngress, c.ConstraintType)
	assert.Equal(t, types.SeverityCritical, c.Severity)
	assert.Equal(t, "deny", c.Effect)
	assert.Contains(t, c.Summary, "denies all traffic")

	// Check details
	require.NotNil(t, c.Details)
	assert.True(t, c.Details["deniesAll"].(bool))
}

func TestParse_MissingSpec(t *testing.T) {
	a := New()
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      "bad-policy",
				"namespace": "default",
			},
		},
	}

	_, err := a.Parse(context.Background(), obj)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing spec")
}

func TestUniqueStrings(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b", "d"}
	result := uniqueStrings(input)

	assert.Len(t, result, 4)
	assert.Contains(t, result, "a")
	assert.Contains(t, result, "b")
	assert.Contains(t, result, "c")
	assert.Contains(t, result, "d")
}
