//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/nightjarctl/nightjar/internal/annotations"
)

// TestClusterScopedConstraintAnnotation verifies that cluster-scoped constraints
// (like ValidatingWebhookConfiguration) trigger workload annotation updates
// without requiring a namespace-scoped constraint as a trigger.
//
// This is the regression test for issue #39:
// https://github.com/cendertdev/nightjar/issues/39
//
// Strategy: Create a deployment first, then create a cluster-scoped webhook.
// The webhook triggers a cluster-wide re-annotation via the sentinel, which
// discovers the already-existing deployment and annotates it.
func (s *E2ESuite) TestClusterScopedConstraintAnnotation() {
	t := s.T()
	ctx := context.Background()

	// 1. Create a deployment in the test namespace FIRST, so it exists when
	// the cluster-wide sentinel scans for workloads.
	depGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	dep := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "annotator-test",
				"namespace": s.namespace,
				"labels": map[string]interface{}{
					e2eLabel: "true",
				},
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": "annotator-test",
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": "annotator-test",
						},
					},
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "pause",
								"image": "registry.k8s.io/pause:3.9",
							},
						},
					},
				},
			},
		},
	}
	_, err := s.dynamicClient.Resource(depGVR).Namespace(s.namespace).Create(ctx, dep, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create test deployment")
	t.Cleanup(func() {
		_ = s.dynamicClient.Resource(depGVR).Namespace(s.namespace).Delete(ctx, "annotator-test", metav1.DeleteOptions{})
	})

	// Wait for deployment to be ready before creating the webhook trigger.
	waitForDeploymentReady(t, s.clientset, s.namespace, "annotator-test", defaultTimeout)

	// Wait for the namespace workload cache to expire (TTL=30s) in case any
	// prior cluster-wide sentinel already cached an empty workload list for
	// this new namespace before the deployment existed.
	t.Log("Waiting 35s for namespace workload cache TTL to expire...")
	time.Sleep(35 * time.Second)

	// 2. Create a unique cluster-scoped ValidatingWebhookConfiguration.
	// This has no namespace and no AffectedNamespaces — the exact scenario from issue #39.
	// Creating it triggers the cluster-wide sentinel in OnIndexChange, which
	// lists all namespaces, discovers the test namespace, and annotates the deployment.
	webhookName := fmt.Sprintf("e2e-test-webhook-%s", s.namespace)
	vwhcGVR := schema.GroupVersionResource{
		Group:    "admissionregistration.k8s.io",
		Version:  "v1",
		Resource: "validatingwebhookconfigurations",
	}
	webhook := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "ValidatingWebhookConfiguration",
			"metadata": map[string]interface{}{
				"name": webhookName,
				"labels": map[string]interface{}{
					e2eLabel: "true",
				},
			},
			"webhooks": []interface{}{
				map[string]interface{}{
					"name":                    "e2e-test.example.io",
					"admissionReviewVersions": []interface{}{"v1"},
					"sideEffects":             "None",
					"failurePolicy":           "Ignore",
					"clientConfig": map[string]interface{}{
						"url": "https://localhost:9443/validate",
					},
					"rules": []interface{}{
						map[string]interface{}{
							"apiGroups":   []interface{}{""},
							"apiVersions": []interface{}{"v1"},
							"operations":  []interface{}{"CREATE"},
							"resources":   []interface{}{"configmaps"},
						},
					},
				},
			},
		},
	}
	_, err = s.dynamicClient.Resource(vwhcGVR).Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create test ValidatingWebhookConfiguration")
	t.Cleanup(func() {
		_ = s.dynamicClient.Resource(vwhcGVR).Delete(ctx, webhookName, metav1.DeleteOptions{})
	})
	t.Log("Created cluster-scoped ValidatingWebhookConfiguration (no namespace, no AffectedNamespaces)")

	t.Log("Waiting for workload annotations to be updated by the annotator...")

	// 3. Wait for the deployment to receive nightjar annotations.
	// Before the fix for #39, this would never happen because OnIndexChange
	// silently dropped cluster-scoped constraints with no AffectedNamespaces.
	waitForCondition(t, 90*time.Second, 2*time.Second, func() (bool, error) {
		dep, err := s.dynamicClient.Resource(depGVR).Namespace(s.namespace).Get(ctx, "annotator-test", metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get deployment: %w", err)
		}
		annots := dep.GetAnnotations()
		if annots == nil {
			return false, nil
		}
		_, hasStatus := annots[annotations.WorkloadStatus]
		return hasStatus, nil
	})

	// 5. Verify the annotations are correct.
	dep, err = s.dynamicClient.Resource(depGVR).Namespace(s.namespace).Get(ctx, "annotator-test", metav1.GetOptions{})
	require.NoError(t, err)

	annots := dep.GetAnnotations()
	require.NotNil(t, annots, "deployment should have annotations")

	// The status should indicate at least one constraint.
	status := annots[annotations.WorkloadStatus]
	assert.NotEmpty(t, status, "nightjar.io/status should be set")
	assert.Contains(t, status, "constraint", "status should mention constraints")
	t.Logf("Workload status: %s", status)

	// The constraints JSON should contain the webhook.
	constraintsJSON := annots[annotations.WorkloadConstraints]
	assert.NotEmpty(t, constraintsJSON, "nightjar.io/constraints should be set")

	var summaries []map[string]interface{}
	err = json.Unmarshal([]byte(constraintsJSON), &summaries)
	require.NoError(t, err, "constraints annotation should be valid JSON")

	// Find the webhook constraint in the list.
	found := false
	for _, s := range summaries {
		source, _ := s["source"].(string)
		if source == "validatingwebhookconfigurations" {
			found = true
			break
		}
	}
	assert.True(t, found, "constraints should include the ValidatingWebhookConfiguration; got: %s", constraintsJSON)

	// Max severity should be set.
	maxSeverity := annots[annotations.WorkloadMaxSeverity]
	assert.NotEmpty(t, maxSeverity, "nightjar.io/max-severity should be set")

	// Last evaluated should be set.
	lastEval := annots[annotations.WorkloadLastEvaluated]
	assert.NotEmpty(t, lastEval, "nightjar.io/last-evaluated should be set")

	t.Logf("Cluster-scoped constraint annotation test passed: workload annotated with %d constraints", len(summaries))
}
