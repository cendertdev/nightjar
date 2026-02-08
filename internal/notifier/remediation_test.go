package notifier

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/nightjarctl/nightjar/internal/types"
)

func TestRemediationBuilder_BuildNetworkPolicy(t *testing.T) {
	rb := NewRemediationBuilder("platform-team@example.com")

	c := types.Constraint{
		UID:       k8stypes.UID("test-uid"),
		Name:      "restrict-egress",
		Namespace: "my-namespace",
		Source: schema.GroupVersionResource{
			Group:    "networking.k8s.io",
			Version:  "v1",
			Resource: "networkpolicies",
		},
		ConstraintType:  types.ConstraintTypeNetworkEgress,
		Effect:          "restrict",
		RemediationHint: "Contact platform team for egress exceptions",
	}

	result := rb.Build(c)

	assert.NotEmpty(t, result.Summary)
	require.GreaterOrEqual(t, len(result.Steps), 3)

	// First step should be kubectl get
	assert.Equal(t, "kubectl", result.Steps[0].Type)
	assert.Contains(t, result.Steps[0].Command, "kubectl get networkpolicy")
	assert.Contains(t, result.Steps[0].Command, "restrict-egress")
	assert.Contains(t, result.Steps[0].Command, "my-namespace")
	assert.Equal(t, "developer", result.Steps[0].RequiresPrivilege)

	// Should have a manual step with contact
	hasManualStep := false
	for _, step := range result.Steps {
		if step.Type == "manual" {
			hasManualStep = true
			assert.NotEmpty(t, step.Contact)
		}
	}
	assert.True(t, hasManualStep, "Should have a manual remediation step")

	// Should have a link step
	hasLinkStep := false
	for _, step := range result.Steps {
		if step.Type == "link" {
			hasLinkStep = true
			assert.NotEmpty(t, step.URL)
		}
	}
	assert.True(t, hasLinkStep, "Should have a documentation link step")
}

func TestRemediationBuilder_BuildResourceQuota(t *testing.T) {
	rb := NewRemediationBuilder("quota-admin@example.com")

	c := types.Constraint{
		UID:       k8stypes.UID("quota-uid"),
		Name:      "compute-quota",
		Namespace: "team-alpha",
		Source: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "resourcequotas",
		},
		ConstraintType: types.ConstraintTypeResourceLimit,
		Effect:         "limit",
		Details: map[string]interface{}{
			"resources": map[string]interface{}{
				"cpu": map[string]interface{}{
					"hard":    "4",
					"used":    "3.5",
					"percent": 87,
				},
			},
		},
	}

	result := rb.Build(c)

	assert.NotEmpty(t, result.Summary)
	require.GreaterOrEqual(t, len(result.Steps), 3)

	// First step should be describe quota
	assert.Equal(t, "kubectl", result.Steps[0].Type)
	assert.Contains(t, result.Steps[0].Command, "kubectl describe resourcequota")
	assert.Contains(t, result.Steps[0].Command, "compute-quota")
	assert.Contains(t, result.Steps[0].Command, "team-alpha")

	// Second step should list pods with resources
	assert.Equal(t, "kubectl", result.Steps[1].Type)
	assert.Contains(t, result.Steps[1].Command, "kubectl get pods")
}

func TestRemediationBuilder_BuildWebhook(t *testing.T) {
	rb := NewRemediationBuilder("security-team@example.com")

	c := types.Constraint{
		UID:       k8stypes.UID("webhook-uid"),
		Name:      "pod-security-webhook-validate-pods",
		Namespace: "",
		Source: schema.GroupVersionResource{
			Group:    "admissionregistration.k8s.io",
			Version:  "v1",
			Resource: "validatingwebhookconfigurations",
		},
		ConstraintType: types.ConstraintTypeAdmission,
		Effect:         "intercept",
	}

	result := rb.Build(c)

	assert.NotEmpty(t, result.Summary)
	require.GreaterOrEqual(t, len(result.Steps), 3)

	// First step should inspect webhook config
	assert.Equal(t, "kubectl", result.Steps[0].Type)
	assert.Contains(t, result.Steps[0].Command, "validatingwebhookconfigurations")
	assert.Equal(t, "cluster-admin", result.Steps[0].RequiresPrivilege)

	// Should have dry-run step
	hasDryRun := false
	for _, step := range result.Steps {
		if step.Type == "kubectl" && strings.Contains(step.Command, "dry-run") {
			hasDryRun = true
			break
		}
	}
	assert.True(t, hasDryRun, "Should have a dry-run kubectl step")
}

func TestRemediationBuilder_BuildMissingResource(t *testing.T) {
	rb := NewRemediationBuilder("platform@example.com")

	c := types.Constraint{
		UID:       k8stypes.UID("missing-uid"),
		Name:      "missing-servicemonitor",
		Namespace: "app-namespace",
		Source: schema.GroupVersionResource{
			Group:    "nightjar.io",
			Version:  "v1alpha1",
			Resource: "missingresourcedetections",
		},
		ConstraintType:  types.ConstraintTypeMissing,
		Effect:          "missing",
		RemediationHint: "Create a ServiceMonitor for metrics scraping",
		Details: map[string]interface{}{
			"expectedKind":       "ServiceMonitor",
			"expectedAPIVersion": "monitoring.coreos.com/v1",
		},
	}

	result := rb.Build(c)

	assert.NotEmpty(t, result.Summary)
	require.GreaterOrEqual(t, len(result.Steps), 2)

	// Should have check step
	assert.Equal(t, "kubectl", result.Steps[0].Type)
	assert.Contains(t, result.Steps[0].Command, "servicemonitor")

	// Should have yaml_patch step with template
	hasTemplate := false
	for _, step := range result.Steps {
		if step.Type == "yaml_patch" {
			hasTemplate = true
			assert.Contains(t, step.Template, "ServiceMonitor")
			assert.Contains(t, step.Template, "{workload_name}")
			assert.Contains(t, step.Template, "{namespace}")
		}
	}
	assert.True(t, hasTemplate, "Should have YAML template for ServiceMonitor")
}

func TestRemediationBuilder_ConvertExistingSteps(t *testing.T) {
	rb := NewRemediationBuilder("default@example.com")

	c := types.Constraint{
		UID:             k8stypes.UID("existing-uid"),
		Name:            "custom-policy",
		Namespace:       "test-ns",
		ConstraintType:  types.ConstraintTypeAdmission,
		RemediationHint: "Follow the custom steps below",
		Remediation: []types.RemediationStep{
			{
				Type:              "kubectl",
				Description:       "Check policy status",
				Command:           "kubectl get policy custom-policy -o yaml",
				RequiresPrivilege: "developer",
			},
			{
				Type:              "manual",
				Description:       "Request exception",
				Contact:           "custom-contact@example.com",
				RequiresPrivilege: "namespace-admin",
			},
		},
	}

	result := rb.Build(c)

	assert.Equal(t, "Follow the custom steps below", result.Summary)
	require.Len(t, result.Steps, 2)

	assert.Equal(t, "kubectl", result.Steps[0].Type)
	assert.Equal(t, "kubectl get policy custom-policy -o yaml", result.Steps[0].Command)

	assert.Equal(t, "manual", result.Steps[1].Type)
	assert.Equal(t, "custom-contact@example.com", result.Steps[1].Contact)
}

func TestRemediationBuilder_GenericRemediation(t *testing.T) {
	rb := NewRemediationBuilder("help@example.com")

	c := types.Constraint{
		UID:       k8stypes.UID("generic-uid"),
		Name:      "unknown-constraint",
		Namespace: "test-ns",
		Source: schema.GroupVersionResource{
			Group:    "custom.io",
			Version:  "v1",
			Resource: "customconstraints",
		},
		ConstraintType: types.ConstraintTypeUnknown,
		Effect:         "unknown",
	}

	result := rb.Build(c)

	assert.NotEmpty(t, result.Summary)
	require.GreaterOrEqual(t, len(result.Steps), 2)

	// Should have kubectl inspect step
	assert.Equal(t, "kubectl", result.Steps[0].Type)
	assert.Contains(t, result.Steps[0].Command, "customconstraints")

	// Should have manual contact step
	hasManual := false
	for _, step := range result.Steps {
		if step.Type == "manual" {
			hasManual = true
			assert.Equal(t, "help@example.com", step.Contact)
		}
	}
	assert.True(t, hasManual)
}

func TestRemediationBuilder_ClusterScopedWebhook(t *testing.T) {
	rb := NewRemediationBuilder("security@example.com")

	c := types.Constraint{
		UID:       k8stypes.UID("mutating-uid"),
		Name:      "inject-sidecar-inject-sidecar",
		Namespace: "",
		Source: schema.GroupVersionResource{
			Group:    "admissionregistration.k8s.io",
			Version:  "v1",
			Resource: "mutatingwebhookconfigurations",
		},
		ConstraintType: types.ConstraintTypeAdmission,
		Effect:         "intercept",
	}

	result := rb.Build(c)

	// Should reference mutating webhook config
	assert.Equal(t, "kubectl", result.Steps[0].Type)
	assert.Contains(t, result.Steps[0].Command, "mutatingwebhookconfigurations")
}

func TestRemediationBuilder_MissingResourceTemplates(t *testing.T) {
	rb := NewRemediationBuilder("platform@example.com")

	testCases := []struct {
		kind             string
		expectedContains []string
	}{
		{"ServiceMonitor", []string{"monitoring.coreos.com", "endpoints", "interval"}},
		{"VirtualService", []string{"networking.istio.io", "http", "route"}},
		{"DestinationRule", []string{"networking.istio.io", "trafficPolicy", "ISTIO_MUTUAL"}},
		{"PodDisruptionBudget", []string{"policy/v1", "minAvailable", "selector"}},
		{"HorizontalPodAutoscaler", []string{"autoscaling/v2", "scaleTargetRef", "minReplicas"}},
	}

	for _, tc := range testCases {
		t.Run(tc.kind, func(t *testing.T) {
			c := types.Constraint{
				UID:            k8stypes.UID("test-" + tc.kind),
				Name:           "missing-" + tc.kind,
				Namespace:      "test-ns",
				ConstraintType: types.ConstraintTypeMissing,
				Details: map[string]interface{}{
					"expectedKind": tc.kind,
				},
			}

			result := rb.Build(c)

			hasTemplate := false
			for _, step := range result.Steps {
				if step.Type == "yaml_patch" && step.Template != "" {
					hasTemplate = true
					for _, expected := range tc.expectedContains {
						assert.Contains(t, step.Template, expected,
							"Template for %s should contain %s", tc.kind, expected)
					}
				}
			}
			assert.True(t, hasTemplate, "Should have template for %s", tc.kind)
		})
	}
}

func TestRemediationBuilder_ResolveContact(t *testing.T) {
	rb := NewRemediationBuilder("default-contact@example.com")

	tests := []struct {
		name     string
		c        types.Constraint
		expected string
	}{
		{
			name: "uses contact from details",
			c: types.Constraint{
				Details: map[string]interface{}{
					"contact": "specific@example.com",
				},
			},
			expected: "specific@example.com",
		},
		{
			name: "falls back to hint with email",
			c: types.Constraint{
				RemediationHint: "Contact team@example.com",
			},
			expected: "Contact team@example.com",
		},
		{
			name: "falls back to hint with slack",
			c: types.Constraint{
				RemediationHint: "Reach out on #platform-help",
			},
			expected: "Reach out on #platform-help",
		},
		{
			name:     "uses default when no contact info",
			c:        types.Constraint{},
			expected: "default-contact@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rb.resolveContact(tt.c)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsSourceHelpers(t *testing.T) {
	t.Run("isNetworkPolicySource", func(t *testing.T) {
		assert.True(t, isNetworkPolicySource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "networkpolicies"},
		}))
		assert.True(t, isNetworkPolicySource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "ciliumnetworkpolicies"},
		}))
		assert.True(t, isNetworkPolicySource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "ciliumclusterwidenetworkpolicies"},
		}))
		assert.False(t, isNetworkPolicySource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "resourcequotas"},
		}))
	})

	t.Run("isResourceQuotaSource", func(t *testing.T) {
		assert.True(t, isResourceQuotaSource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "resourcequotas"},
		}))
		assert.True(t, isResourceQuotaSource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "limitranges"},
		}))
		assert.False(t, isResourceQuotaSource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "networkpolicies"},
		}))
	})

	t.Run("isWebhookSource", func(t *testing.T) {
		assert.True(t, isWebhookSource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "validatingwebhookconfigurations"},
		}))
		assert.True(t, isWebhookSource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "mutatingwebhookconfigurations"},
		}))
		assert.False(t, isWebhookSource(types.Constraint{
			Source: schema.GroupVersionResource{Resource: "networkpolicies"},
		}))
	})
}
