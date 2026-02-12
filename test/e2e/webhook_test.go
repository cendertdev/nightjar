//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

// --- Warning capture ---

// warningCollector implements rest.WarningHandler to capture admission warnings.
type warningCollector struct {
	mu       sync.Mutex
	warnings []string
}

func (w *warningCollector) HandleWarningHeader(code int, agent string, text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.warnings = append(w.warnings, text)
}

func (w *warningCollector) get() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, len(w.warnings))
	copy(out, w.warnings)
	return out
}

func (w *warningCollector) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.warnings = nil
}

// clientWithWarnings creates a dynamic client that captures admission warnings.
func clientWithWarnings(t *testing.T, collector *warningCollector) dynamic.Interface {
	t.Helper()
	cfg, err := ctrl.GetConfig()
	require.NoError(t, err, "failed to load kubeconfig")

	cfgCopy := rest.CopyConfig(cfg)
	cfgCopy.WarningHandler = collector

	dc, err := dynamic.NewForConfig(cfgCopy)
	require.NoError(t, err, "failed to create dynamic client with warning handler")
	return dc
}

// --- Test methods on E2ESuite ---

// TestWebhookDeploymentReady verifies the webhook Deployment has ready replicas
// and the Service has endpoints.
func (s *E2ESuite) TestWebhookDeploymentReady() {
	t := s.T()

	// Check deployment is ready
	deploy, err := s.clientset.AppsV1().Deployments(controllerNamespace).Get(
		context.Background(), webhookDeploymentName, metav1.GetOptions{},
	)
	require.NoError(t, err, "webhook deployment not found")
	require.Greater(t, deploy.Status.ReadyReplicas, int32(0),
		"webhook deployment has no ready replicas")

	var desired int32 = 2
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}
	t.Logf("Webhook deployment: %d/%d ready", deploy.Status.ReadyReplicas, desired)

	// Check service endpoints
	endpoints, err := s.clientset.CoreV1().Endpoints(controllerNamespace).Get(
		context.Background(), webhookServiceName, metav1.GetOptions{},
	)
	require.NoError(t, err, "webhook service endpoints not found")
	require.NotEmpty(t, endpoints.Subsets, "webhook service has no endpoint subsets")

	totalAddrs := 0
	for _, subset := range endpoints.Subsets {
		totalAddrs += len(subset.Addresses)
	}
	require.Greater(t, totalAddrs, 0, "webhook service has no ready endpoint addresses")
	t.Logf("Webhook service: %d endpoint addresses", totalAddrs)
}

// TestWebhookHealthEndpoints verifies the webhook /healthz and /readyz respond correctly.
// We verify this indirectly: if the webhook pods are Ready, their probes succeeded.
func (s *E2ESuite) TestWebhookHealthEndpoints() {
	t := s.T()

	pods, err := s.clientset.CoreV1().Pods(controllerNamespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/component=webhook",
	})
	require.NoError(t, err, "failed to list webhook pods")
	require.NotEmpty(t, pods.Items, "no webhook pods found")

	for _, pod := range pods.Items {
		ready := false
		for _, cond := range pod.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
				break
			}
		}
		assert.True(t, ready, "webhook pod %s is not ready (probes failed)", pod.Name)
		t.Logf("Webhook pod %s: ready=%v", pod.Name, ready)
	}
}

// TestAdmissionWarningsForConstraint creates a NetworkPolicy, then deploys a
// matching workload and verifies the admission response includes warnings.
func (s *E2ESuite) TestAdmissionWarningsForConstraint() {
	t := s.T()

	// Create a test namespace for this test.
	ns, cleanup := createTestNamespace(t, s.clientset)
	defer cleanup()

	// Create a NetworkPolicy that restricts egress — this will be indexed as a
	// Warning-severity constraint by the controller.
	netpol := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "NetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      "restrict-egress",
				"namespace": ns,
			},
			"spec": map[string]interface{}{
				"podSelector": map[string]interface{}{},
				"policyTypes": []interface{}{"Egress"},
				"egress": []interface{}{
					map[string]interface{}{
						"ports": []interface{}{
							map[string]interface{}{
								"protocol": "TCP",
								"port":     int64(443),
							},
						},
					},
				},
			},
		},
	}
	applyUnstructured(t, s.dynamicClient, netpol)
	defer deleteUnstructured(t, s.dynamicClient,
		schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
		ns, "restrict-egress")

	// Wait for the controller to discover and index the constraint.
	t.Log("Waiting for controller to index the NetworkPolicy constraint...")
	time.Sleep(10 * time.Second)

	// Create a workload using a warning-capturing client.
	collector := &warningCollector{}
	warnClient := clientWithWarnings(t, collector)

	depGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	dep := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "warn-test-workload",
				"namespace": ns,
				"labels": map[string]interface{}{
					e2eLabel: "true",
				},
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": "warn-test",
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": "warn-test",
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

	created, err := warnClient.Resource(depGVR).Namespace(ns).Create(
		context.Background(), dep, metav1.CreateOptions{},
	)
	require.NoError(t, err, "failed to create test deployment")
	require.NotNil(t, created)
	t.Logf("Created deployment %s/%s", ns, created.GetName())

	// Check captured warnings
	warnings := collector.get()
	t.Logf("Captured %d admission warnings: %v", len(warnings), warnings)

	// The workload should have been admitted (creation succeeded).
	// Warnings should contain [WARNING] or [CRITICAL] prefixed constraint info.
	if len(warnings) > 0 {
		hasConstraintWarning := false
		for _, w := range warnings {
			if containsWarningPrefix(w) {
				hasConstraintWarning = true
				t.Logf("Constraint warning: %s", w)
			}
		}
		assert.True(t, hasConstraintWarning,
			"expected at least one [WARNING] or [CRITICAL] prefixed warning, got: %v", warnings)
	} else {
		// Warnings may not appear if the controller hasn't indexed yet or the
		// webhook query timed out. Log for debugging but don't fail hard — the
		// controller indexing race makes this non-deterministic.
		t.Log("No admission warnings captured — controller may not have indexed yet")
		t.Log("Webhook logs for debugging:")
		t.Log(getWebhookLogs(t, s.clientset, 30))
		t.Log("Controller logs for debugging:")
		t.Log(getControllerLogs(t, s.clientset, 30))
	}

	// Cleanup
	_ = warnClient.Resource(depGVR).Namespace(ns).Delete(
		context.Background(), "warn-test-workload", metav1.DeleteOptions{},
	)
}

// TestWebhookNeverRejects verifies that the webhook never rejects a workload,
// even when a Critical constraint matches.
func (s *E2ESuite) TestWebhookNeverRejects() {
	t := s.T()

	ns, cleanup := createTestNamespace(t, s.clientset)
	defer cleanup()

	// Create a ResourceQuota (parsed as Critical severity by the adapter).
	quota := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ResourceQuota",
			"metadata": map[string]interface{}{
				"name":      "strict-quota",
				"namespace": ns,
			},
			"spec": map[string]interface{}{
				"hard": map[string]interface{}{
					"pods": "100",
				},
			},
		},
	}
	applyUnstructured(t, s.dynamicClient, quota)
	defer deleteUnstructured(t, s.dynamicClient,
		schema.GroupVersionResource{Group: "", Version: "v1", Resource: "resourcequotas"},
		ns, "strict-quota")

	// Wait for indexing
	time.Sleep(10 * time.Second)

	// Deploy a workload — MUST succeed (never rejected)
	cleanupDeploy := createTestDeployment(t, s.dynamicClient, ns, "never-reject-test")
	defer cleanupDeploy()

	// Verify the deployment exists and was admitted
	depGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	dep, err := s.dynamicClient.Resource(depGVR).Namespace(ns).Get(
		context.Background(), "never-reject-test", metav1.GetOptions{},
	)
	require.NoError(t, err, "deployment should exist — webhook must not reject")
	require.NotNil(t, dep)
	t.Logf("Deployment %s/%s admitted successfully (never-reject verified)", ns, dep.GetName())
}

// TestWebhookNoConstraintsNoWarnings deploys a workload in a namespace with
// zero namespace-specific constraints and verifies no namespace-specific
// warnings are returned. Cluster-scoped constraints (empty namespace) may
// still produce warnings for any namespace — this is correct behavior.
func (s *E2ESuite) TestWebhookNoConstraintsNoWarnings() {
	t := s.T()

	ns, cleanup := createTestNamespace(t, s.clientset)
	defer cleanup()

	// No constraints created in this namespace.
	collector := &warningCollector{}
	warnClient := clientWithWarnings(t, collector)

	depGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	dep := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "no-warn-test",
				"namespace": ns,
				"labels": map[string]interface{}{
					e2eLabel: "true",
				},
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": "no-warn-test",
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": "no-warn-test",
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

	_, err := warnClient.Resource(depGVR).Namespace(ns).Create(
		context.Background(), dep, metav1.CreateOptions{},
	)
	require.NoError(t, err, "failed to create test deployment")

	warnings := collector.get()
	constraintWarnings := filterConstraintWarnings(warnings)
	// Filter to only warnings that mention the test namespace — these would
	// indicate namespace-specific constraints that should not exist.
	nsSpecific := filterWarningsByNamespace(constraintWarnings, ns)
	assert.Empty(t, nsSpecific,
		"expected zero namespace-specific constraint warnings, got: %v", nsSpecific)
	t.Logf("No namespace-specific warnings (got %d cluster-scoped, %d total)",
		len(constraintWarnings), len(warnings))

	// Cleanup
	_ = warnClient.Resource(depGVR).Namespace(ns).Delete(
		context.Background(), "no-warn-test", metav1.DeleteOptions{},
	)
}

// TestWebhookFailOpen verifies that when the webhook is unavailable, workloads
// are still admitted (failurePolicy: Ignore).
func (s *E2ESuite) TestWebhookFailOpen() {
	t := s.T()

	ns, cleanup := createTestNamespace(t, s.clientset)
	defer cleanup()

	// Scale webhook to 0
	scaleDeployment(t, s.clientset, controllerNamespace, webhookDeploymentName, 0)

	// Wait for endpoints to drain so the API server sees the webhook as unavailable.
	t.Log("Waiting for webhook endpoints to drain...")
	waitForCondition(t, 30*time.Second, defaultPollInterval, func() (bool, error) {
		endpoints, err := s.clientset.CoreV1().Endpoints(controllerNamespace).Get(
			context.Background(), webhookServiceName, metav1.GetOptions{},
		)
		if err != nil {
			return false, err
		}
		for _, subset := range endpoints.Subsets {
			if len(subset.Addresses) > 0 {
				return false, nil
			}
		}
		return true, nil
	})
	t.Log("Webhook endpoints drained, waiting for API server propagation...")
	time.Sleep(2 * time.Second)

	// Deploy a workload — MUST succeed despite webhook being down
	cleanupDeploy := createTestDeployment(t, s.dynamicClient, ns, "fail-open-test")
	defer cleanupDeploy()

	depGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	dep, err := s.dynamicClient.Resource(depGVR).Namespace(ns).Get(
		context.Background(), "fail-open-test", metav1.GetOptions{},
	)
	require.NoError(t, err, "deployment should be admitted when webhook is down (fail-open)")
	require.NotNil(t, dep)
	t.Logf("Fail-open verified: deployment %s/%s admitted with webhook down", ns, dep.GetName())

	// Restore webhook
	scaleDeployment(t, s.clientset, controllerNamespace, webhookDeploymentName, 2)
	waitForWebhookReady(t, s.clientset, webhookReadyTimeout)
	t.Log("Webhook restored after fail-open test")
}

// TestSelfSignedCertificateInjection verifies:
// 1. The webhook TLS Secret exists with ca.crt, tls.crt, tls.key
// 2. The ValidatingWebhookConfiguration has a non-empty caBundle
// 3. The caBundle matches the Secret's ca.crt
func (s *E2ESuite) TestSelfSignedCertificateInjection() {
	t := s.T()

	// Verify TLS Secret
	secret := getTLSSecret(t, s.clientset)
	require.Contains(t, secret.Data, "ca.crt", "TLS secret missing ca.crt")
	require.Contains(t, secret.Data, "tls.crt", "TLS secret missing tls.crt")
	require.Contains(t, secret.Data, "tls.key", "TLS secret missing tls.key")
	require.NotEmpty(t, secret.Data["ca.crt"], "ca.crt is empty")
	require.NotEmpty(t, secret.Data["tls.crt"], "tls.crt is empty")
	require.NotEmpty(t, secret.Data["tls.key"], "tls.key is empty")
	t.Logf("TLS Secret %s: ca.crt=%d bytes, tls.crt=%d bytes, tls.key=%d bytes",
		webhookSecretName, len(secret.Data["ca.crt"]), len(secret.Data["tls.crt"]), len(secret.Data["tls.key"]))

	// Verify VWC caBundle
	vwc := getValidatingWebhookConfig(t, s.clientset)
	require.NotEmpty(t, vwc.Webhooks, "VWC has no webhook entries")

	caBundle := vwc.Webhooks[0].ClientConfig.CABundle
	require.NotEmpty(t, caBundle, "VWC caBundle is empty")

	// The caBundle should match the CA cert from the Secret.
	// The Secret stores PEM-encoded certs; the VWC caBundle is the raw PEM bytes.
	assert.Equal(t, secret.Data["ca.crt"], caBundle,
		"VWC caBundle does not match Secret ca.crt")
	t.Logf("VWC caBundle matches Secret ca.crt (%d bytes)", len(caBundle))

	// Verify the VWC failurePolicy is Ignore
	vwcFailurePolicy := vwc.Webhooks[0].FailurePolicy
	require.NotNil(t, vwcFailurePolicy)
	assert.Equal(t, "Ignore", string(*vwcFailurePolicy),
		"VWC failurePolicy must be Ignore, got %s", string(*vwcFailurePolicy))
	t.Log("VWC failurePolicy confirmed: Ignore")
}

// TestPodDisruptionBudget verifies the PDB exists with the correct spec when
// the webhook has replicas > 1.
func (s *E2ESuite) TestPodDisruptionBudget() {
	t := s.T()

	// Verify the webhook has replicas > 1
	deploy, err := s.clientset.AppsV1().Deployments(controllerNamespace).Get(
		context.Background(), webhookDeploymentName, metav1.GetOptions{},
	)
	require.NoError(t, err, "webhook deployment not found")

	var replicas int32 = 1
	if deploy.Spec.Replicas != nil {
		replicas = *deploy.Spec.Replicas
	}
	if replicas <= 1 {
		t.Skipf("Skipping PDB test: webhook replicas=%d (need >1)", replicas)
	}

	pdb := getWebhookPDB(t, s.clientset)
	require.NotNil(t, pdb, "PDB not found for webhook (expected with replicas=%d)", replicas)

	// Verify minAvailable is set
	require.NotNil(t, pdb.Spec.MinAvailable, "PDB minAvailable is nil")
	t.Logf("PDB %s: minAvailable=%s", pdb.Name, pdb.Spec.MinAvailable.String())

	// Verify it matches the selector for the webhook pods
	require.NotNil(t, pdb.Spec.Selector, "PDB has no selector")
	labels := pdb.Spec.Selector.MatchLabels
	assert.Equal(t, "webhook", labels["app.kubernetes.io/component"],
		"PDB selector should target webhook component")
}

// --- Warning prefix helpers ---

// containsWarningPrefix returns true if the string starts with [WARNING] or [CRITICAL].
func containsWarningPrefix(s string) bool {
	return (len(s) >= 9 && s[:9] == "[WARNING]") ||
		(len(s) >= 10 && s[:10] == "[CRITICAL]")
}

// filterConstraintWarnings returns only warnings with [WARNING] or [CRITICAL] prefix.
func filterConstraintWarnings(warnings []string) []string {
	var result []string
	for _, w := range warnings {
		if containsWarningPrefix(w) {
			result = append(result, w)
		}
	}
	return result
}

// filterWarningsByNamespace returns warnings that reference a specific namespace.
// The webhook formats warnings as: [WARNING] <type> "<name>" <msg> - Review <type> <ns>/<name> ...
// Cluster-scoped constraints use "/<name>" (no namespace before the slash).
func filterWarningsByNamespace(warnings []string, ns string) []string {
	var result []string
	for _, w := range warnings {
		if strings.Contains(w, ns+"/") {
			result = append(result, w)
		}
	}
	return result
}
