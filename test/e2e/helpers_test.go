//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/nightjarctl/nightjar/internal/annotations"
)

const (
	// testNamespacePrefix is the prefix for test namespace names.
	testNamespacePrefix = "nightjar-e2e-"

	// e2eLabel marks resources created by E2E tests for cleanup.
	e2eLabel = "nightjar-e2e"

	// controllerNamespace is the namespace where the controller is deployed.
	controllerNamespace = "nightjar-system"

	// controllerDeploymentName is the name of the controller deployment.
	controllerDeploymentName = "nightjar-controller"

	// defaultPollInterval is the default interval for polling loops.
	defaultPollInterval = 1 * time.Second

	// defaultTimeout is the default timeout for wait operations.
	defaultTimeout = 60 * time.Second
)

// waitForCondition polls until conditionFn returns true or the timeout expires.
func waitForCondition(t *testing.T, timeout, interval time.Duration, conditionFn func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, err := conditionFn()
		if err != nil {
			t.Logf("waitForCondition: %v", err)
		}
		if ok {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("waitForCondition: timed out after %v", timeout)
}

// waitForControllerReady waits for the controller deployment to have at least
// one ready replica by checking the deployment status via the Kubernetes API.
func waitForControllerReady(t *testing.T, clientset kubernetes.Interface, timeout time.Duration) {
	t.Helper()
	t.Logf("Waiting for controller deployment %s/%s to become ready...", controllerNamespace, controllerDeploymentName)

	waitForCondition(t, timeout, defaultPollInterval, func() (bool, error) {
		deploy, err := clientset.AppsV1().Deployments(controllerNamespace).Get(
			context.Background(), controllerDeploymentName, metav1.GetOptions{},
		)
		if err != nil {
			return false, fmt.Errorf("get deployment: %w", err)
		}
		if deploy.Status.ReadyReplicas > 0 {
			var desired int32 = 1
			if deploy.Spec.Replicas != nil {
				desired = *deploy.Spec.Replicas
			}
			t.Logf("Controller ready: %d/%d replicas", deploy.Status.ReadyReplicas, desired)
			return true, nil
		}
		return false, nil
	})
}

// waitForDeploymentReady waits for a deployment to have all replicas ready.
func waitForDeploymentReady(t *testing.T, clientset kubernetes.Interface, namespace, name string, timeout time.Duration) {
	t.Helper()
	t.Logf("Waiting for deployment %s/%s to become ready...", namespace, name)

	waitForCondition(t, timeout, defaultPollInterval, func() (bool, error) {
		deploy, err := clientset.AppsV1().Deployments(namespace).Get(
			context.Background(), name, metav1.GetOptions{},
		)
		if err != nil {
			return false, fmt.Errorf("get deployment: %w", err)
		}
		if deploy.Spec.Replicas != nil && deploy.Status.ReadyReplicas >= *deploy.Spec.Replicas {
			return true, nil
		}
		return false, nil
	})
}

// createTestNamespace creates a labeled namespace with a random suffix.
// Returns the namespace name and a cleanup function that deletes it.
func createTestNamespace(t *testing.T, clientset kubernetes.Interface) (string, func()) {
	t.Helper()
	name := testNamespacePrefix + rand.String(6)

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				e2eLabel: "true",
			},
		},
	}
	_, err := clientset.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create test namespace %s", name)
	t.Logf("Created test namespace: %s", name)

	cleanup := func() {
		t.Logf("Deleting test namespace: %s", name)
		err := clientset.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{})
		if err != nil {
			t.Logf("Warning: failed to delete namespace %s: %v", name, err)
		}
	}
	return name, cleanup
}

// waitForEvent polls for Kubernetes Events in the given namespace that reference
// the specified involved object name. Returns the matching events.
func waitForEvent(t *testing.T, clientset kubernetes.Interface, namespace, involvedObjectName string, timeout time.Duration) []corev1.Event {
	t.Helper()
	var matched []corev1.Event

	waitForCondition(t, timeout, defaultPollInterval, func() (bool, error) {
		events, err := clientset.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("list events: %w", err)
		}
		matched = nil
		for _, ev := range events.Items {
			if ev.InvolvedObject.Name == involvedObjectName {
				matched = append(matched, ev)
			}
		}
		return len(matched) > 0, nil
	})
	return matched
}

// assertEventExists asserts that at least one Kubernetes Event exists in the
// namespace for the given workload that carries the expected Nightjar annotations.
// expectedAnnotations is a map of annotation key to expected value.
func assertEventExists(t *testing.T, clientset kubernetes.Interface, namespace, workloadName string, expectedAnnotations map[string]string, timeout time.Duration) {
	t.Helper()
	events := waitForEvent(t, clientset, namespace, workloadName, timeout)
	require.NotEmpty(t, events, "no events found for workload %s/%s", namespace, workloadName)

	// Find at least one event matching all expected annotations.
	for _, ev := range events {
		if ev.Annotations == nil {
			continue
		}
		allMatch := true
		for k, v := range expectedAnnotations {
			if ev.Annotations[k] != v {
				allMatch = false
				break
			}
		}
		if allMatch {
			t.Logf("Found matching event: %s (reason=%s)", ev.Name, ev.Reason)
			return
		}
	}

	// No event matched all annotations — report what we found.
	t.Errorf("No event on %s/%s matched all expected annotations %v", namespace, workloadName, expectedAnnotations)
	for i, ev := range events {
		t.Logf("  Event[%d]: reason=%s annotations=%v", i, ev.Reason, ev.Annotations)
	}
	t.FailNow()
}

// assertEventAnnotation asserts that a single event has the given annotation key and value.
func assertEventAnnotation(t *testing.T, event corev1.Event, key, expectedValue string) {
	t.Helper()
	require.NotNil(t, event.Annotations, "event %s has no annotations", event.Name)
	assert.Equal(t, expectedValue, event.Annotations[key],
		"event %s: annotation %s mismatch", event.Name, key)
}

// assertManagedByNightjar asserts that an event is managed by Nightjar.
func assertManagedByNightjar(t *testing.T, event corev1.Event) {
	t.Helper()
	assertEventAnnotation(t, event, annotations.ManagedBy, annotations.ManagedByValue)
}

// applyUnstructured creates or updates an unstructured object in the cluster.
func applyUnstructured(t *testing.T, dynamicClient dynamic.Interface, obj *unstructured.Unstructured) {
	t.Helper()
	gvr := schema.GroupVersionResource{
		Group:    obj.GroupVersionKind().Group,
		Version:  obj.GroupVersionKind().Version,
		Resource: guessResource(obj.GetKind()),
	}

	ns := obj.GetNamespace()
	var err error
	if ns != "" {
		_, err = dynamicClient.Resource(gvr).Namespace(ns).Create(
			context.Background(), obj, metav1.CreateOptions{},
		)
	} else {
		_, err = dynamicClient.Resource(gvr).Create(
			context.Background(), obj, metav1.CreateOptions{},
		)
	}
	require.NoError(t, err, "failed to apply %s %s/%s", obj.GetKind(), ns, obj.GetName())
	t.Logf("Applied %s %s/%s", obj.GetKind(), ns, obj.GetName())
}

// deleteUnstructured deletes an unstructured object from the cluster.
func deleteUnstructured(t *testing.T, dynamicClient dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) {
	t.Helper()
	var err error
	if namespace != "" {
		err = dynamicClient.Resource(gvr).Namespace(namespace).Delete(
			context.Background(), name, metav1.DeleteOptions{},
		)
	} else {
		err = dynamicClient.Resource(gvr).Delete(
			context.Background(), name, metav1.DeleteOptions{},
		)
	}
	if err != nil {
		t.Logf("Warning: failed to delete %s %s/%s: %v", gvr.Resource, namespace, name, err)
	}
}

// getControllerLogs retrieves the logs from the controller pod for debugging.
func getControllerLogs(t *testing.T, clientset kubernetes.Interface, tailLines int64) string {
	t.Helper()
	pods, err := clientset.CoreV1().Pods(controllerNamespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/component=controller",
	})
	if err != nil {
		t.Logf("Warning: failed to list controller pods: %v", err)
		return ""
	}
	if len(pods.Items) == 0 {
		t.Log("Warning: no controller pods found")
		return ""
	}

	pod := pods.Items[0]
	opts := &corev1.PodLogOptions{
		TailLines: &tailLines,
	}
	req := clientset.CoreV1().Pods(controllerNamespace).GetLogs(pod.Name, opts)
	stream, err := req.Stream(context.Background())
	if err != nil {
		t.Logf("Warning: failed to get logs for pod %s: %v", pod.Name, err)
		return ""
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		t.Logf("Warning: failed to read logs for pod %s: %v", pod.Name, err)
		return ""
	}
	return buf.String()
}

// --- Correlation test helpers ---

// correlationEventTimeout is the time to wait for a Nightjar ConstraintNotification event.
// Accounts for: informer sync + adapter parse + indexer upsert + event watch + correlation + dispatch.
const correlationEventTimeout = 60 * time.Second

// workloadAnnotationTimeout is the time to wait for workload annotations to appear.
// Accounts for: indexer upsert + onChange callback + debounce (30s) + patch.
const workloadAnnotationTimeout = 90 * time.Second

// createTestDeployment creates a minimal Deployment using pause:3.9 in the given namespace.
// Returns a cleanup function that deletes the deployment.
func createTestDeployment(t *testing.T, dynamicClient dynamic.Interface, namespace, name string) func() {
	t.Helper()
	depGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	dep := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					e2eLabel: "true",
				},
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": name,
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": name,
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
	_, err := dynamicClient.Resource(depGVR).Namespace(namespace).Create(
		context.Background(), dep, metav1.CreateOptions{},
	)
	require.NoError(t, err, "failed to create test deployment %s/%s", namespace, name)
	t.Logf("Created test deployment: %s/%s", namespace, name)

	return func() {
		_ = dynamicClient.Resource(depGVR).Namespace(namespace).Delete(
			context.Background(), name, metav1.DeleteOptions{},
		)
	}
}

// waitForNightjarEvent polls for Kubernetes Events created by Nightjar (Reason=ConstraintNotification,
// Source.Component=nightjar-controller) that reference the given workload name.
func waitForNightjarEvent(t *testing.T, clientset kubernetes.Interface, namespace, workloadName string, timeout time.Duration) []corev1.Event {
	t.Helper()
	var matched []corev1.Event

	waitForCondition(t, timeout, defaultPollInterval, func() (bool, error) {
		events, err := clientset.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("list events: %w", err)
		}
		matched = nil
		for _, ev := range events.Items {
			if ev.InvolvedObject.Name == workloadName &&
				ev.Reason == "ConstraintNotification" &&
				ev.Source.Component == "nightjar-controller" {
				matched = append(matched, ev)
			}
		}
		return len(matched) > 0, nil
	})
	return matched
}

// getNightjarEvents returns Nightjar ConstraintNotification events for a workload
// without waiting. Use this for counting events after a known wait period.
func getNightjarEvents(t *testing.T, clientset kubernetes.Interface, namespace, workloadName string) []corev1.Event {
	t.Helper()
	events, err := clientset.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err, "failed to list events in %s", namespace)

	var matched []corev1.Event
	for _, ev := range events.Items {
		if ev.InvolvedObject.Name == workloadName &&
			ev.Reason == "ConstraintNotification" &&
			ev.Source.Component == "nightjar-controller" {
			matched = append(matched, ev)
		}
	}
	return matched
}

// waitForNightjarEventByAnnotation polls for Nightjar events that match a specific
// annotation key-value pair. Because the dispatcher rate-limits per namespace and
// drops excess notifications (non-blocking Allow()), a single warning event may not
// produce an event for every constraint. This function periodically re-sends warning
// events to give the rate limiter time to recover and process additional constraints.
func waitForNightjarEventByAnnotation(t *testing.T, clientset kubernetes.Interface, namespace, workloadName, annotKey, annotValue string, timeout time.Duration) []corev1.Event {
	t.Helper()
	var matched []corev1.Event
	deadline := time.Now().Add(timeout)
	retryInterval := 3 * time.Second
	lastWarning := time.Time{}

	for time.Now().Before(deadline) {
		events, err := clientset.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Logf("waitForNightjarEventByAnnotation: list events: %v", err)
			time.Sleep(defaultPollInterval)
			continue
		}
		matched = nil
		for _, ev := range events.Items {
			if ev.InvolvedObject.Name == workloadName &&
				ev.Reason == "ConstraintNotification" &&
				ev.Source.Component == "nightjar-controller" &&
				ev.Annotations != nil &&
				ev.Annotations[annotKey] == annotValue {
				matched = append(matched, ev)
			}
		}
		if len(matched) > 0 {
			return matched
		}

		// Re-send a warning event to trigger another round of correlation.
		// Each new warning has a unique UID, so the correlator re-emits all
		// constraints, giving the rate limiter another chance to process the one we want.
		if time.Since(lastWarning) >= retryInterval {
			createWarningEvent(t, clientset, namespace, workloadName, "Deployment")
			lastWarning = time.Now()
		}

		time.Sleep(defaultPollInterval)
	}

	t.Fatalf("waitForNightjarEventByAnnotation: timed out after %v waiting for %s=%s on workload %s", timeout, annotKey, annotValue, workloadName)
	return nil
}

// createWarningEvent creates a synthetic Warning event referencing the given involved object.
// This triggers the Correlator's event watch (FieldSelector: type=Warning).
func createWarningEvent(t *testing.T, clientset kubernetes.Interface, namespace, involvedName, involvedKind string) *corev1.Event {
	t.Helper()
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-warning-",
			Namespace:    namespace,
			Labels: map[string]string{
				e2eLabel: "true",
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      involvedKind,
			Namespace: namespace,
			Name:      involvedName,
		},
		Reason:         "E2ETestWarning",
		Message:        "Synthetic warning event for E2E correlation testing",
		Type:           corev1.EventTypeWarning,
		Source:         corev1.EventSource{Component: "e2e-test"},
		FirstTimestamp: metav1.Now(),
		LastTimestamp:  metav1.Now(),
		Count:          1,
	}
	created, err := clientset.CoreV1().Events(namespace).Create(context.Background(), event, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create warning event for %s/%s", namespace, involvedName)
	t.Logf("Created warning event: %s", created.Name)
	return created
}

// waitForWorkloadAnnotation polls a deployment until the specified annotation key
// is present, then returns its value.
func waitForWorkloadAnnotation(t *testing.T, dynamicClient dynamic.Interface, namespace, deploymentName, annotationKey string, timeout time.Duration) string {
	t.Helper()
	depGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	var value string

	waitForCondition(t, timeout, defaultPollInterval, func() (bool, error) {
		dep, err := dynamicClient.Resource(depGVR).Namespace(namespace).Get(
			context.Background(), deploymentName, metav1.GetOptions{},
		)
		if err != nil {
			return false, fmt.Errorf("get deployment: %w", err)
		}
		annots := dep.GetAnnotations()
		if annots == nil {
			return false, nil
		}
		v, ok := annots[annotationKey]
		if !ok {
			return false, nil
		}
		value = v
		return true, nil
	})
	return value
}

// waitForStableNightjarEventCount continuously sends warning events at 1s intervals
// to saturate the dispatcher dedup cache across all constraints. The dispatcher
// rate limiter (100/min, burst=10) allows ~1-2 events per warning after the initial
// burst. When all constraints are dedup'd, new warnings produce 0 new events.
//
// Returns the stable count when 15 consecutive seconds of warnings produce no growth.
func waitForStableNightjarEventCount(t *testing.T, clientset kubernetes.Interface, namespace, workloadName string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	t.Log("Sending warnings to saturate dedup cache across all constraints")

	lastCount := 0
	lastGrowth := time.Now()

	for time.Now().Before(deadline) {
		// Send a warning — this triggers the correlator to emit notifications for
		// all constraints. The rate limiter allows ~1.67/sec through. If the
		// constraint is already dedup'd, no event is created.
		createWarningEvent(t, clientset, namespace, workloadName, "Deployment")
		time.Sleep(1 * time.Second)

		events := getNightjarEvents(t, clientset, namespace, workloadName)
		currentCount := len(events)

		if currentCount > lastCount {
			t.Logf("Event count: %d (+%d)", currentCount, currentCount-lastCount)
			lastCount = currentCount
			lastGrowth = time.Now()
		}

		// If 15 seconds of continuous warnings produced no growth,
		// all constraints are in the dedup cache.
		if time.Since(lastGrowth) >= 15*time.Second && lastCount > 0 {
			t.Logf("Event count stable at %d for 15s of continuous warnings", lastCount)
			return lastCount
		}
	}

	t.Fatalf("waitForStableNightjarEventCount: timed out after %v; last count=%d", timeout, lastCount)
	return 0
}

// guessResource converts a Kind name to a plural resource name.
// Handles common Kubernetes kinds; extend as needed.
func guessResource(kind string) string {
	switch kind {
	case "NetworkPolicy":
		return "networkpolicies"
	case "ResourceQuota":
		return "resourcequotas"
	case "LimitRange":
		return "limitranges"
	case "Namespace":
		return "namespaces"
	case "Deployment":
		return "deployments"
	case "StatefulSet":
		return "statefulsets"
	case "DaemonSet":
		return "daemonsets"
	case "Service":
		return "services"
	case "ConfigMap":
		return "configmaps"
	case "Pod":
		return "pods"
	case "ValidatingWebhookConfiguration":
		return "validatingwebhookconfigurations"
	case "MutatingWebhookConfiguration":
		return "mutatingwebhookconfigurations"
	case "CustomResourceDefinition":
		return "customresourcedefinitions"
	default:
		// Best-effort: lowercase + "s"
		return strings.ToLower(kind) + "s"
	}
}

// constraintSummary is a compact representation of a constraint for JSON deserialization.
// Mirrors notifier.ConstraintSummary.
type constraintSummary struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Name     string `json:"name"`
	Source   string `json:"source"`
}

// createSentinelDeployment creates a minimal Deployment in the given namespace
// that the workload annotator can target. Returns a cleanup function.
func createSentinelDeployment(t *testing.T, clientset kubernetes.Interface, namespace, name string) func() {
	t.Helper()
	replicas := int32(1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "pause",
							Image:   "registry.k8s.io/pause:3.9",
							Command: []string{"/pause"},
						},
					},
				},
			},
		},
	}
	_, err := clientset.AppsV1().Deployments(namespace).Create(
		context.Background(), deploy, metav1.CreateOptions{},
	)
	require.NoError(t, err, "failed to create sentinel deployment %s/%s", namespace, name)
	t.Logf("Created sentinel deployment: %s/%s", namespace, name)

	return func() {
		err := clientset.AppsV1().Deployments(namespace).Delete(
			context.Background(), name, metav1.DeleteOptions{},
		)
		if err != nil {
			t.Logf("Warning: failed to delete sentinel deployment %s/%s: %v", namespace, name, err)
		}
	}
}

// --- Webhook test helpers ---

const (
	// webhookDeploymentName is the name of the webhook Deployment in the E2E cluster.
	webhookDeploymentName = "nightjar-webhook"

	// webhookServiceName is the name of the webhook Service.
	webhookServiceName = "nightjar-webhook"

	// webhookConfigName is the name of the ValidatingWebhookConfiguration.
	webhookConfigName = "nightjar-webhook"

	// webhookSecretName is the name of the TLS Secret for self-signed certs.
	webhookSecretName = "nightjar-webhook-tls"

	// webhookReadyTimeout is time to wait for the webhook to become fully ready
	// (deployment + VWC caBundle injected).
	webhookReadyTimeout = 120 * time.Second
)

// waitForWebhookReady waits for the webhook Deployment to have ready replicas
// AND the ValidatingWebhookConfiguration to have a non-empty caBundle.
func waitForWebhookReady(t *testing.T, clientset kubernetes.Interface, timeout time.Duration) {
	t.Helper()
	t.Logf("Waiting for webhook deployment %s/%s to become ready...", controllerNamespace, webhookDeploymentName)

	// First wait for Deployment readiness.
	waitForDeploymentReady(t, clientset, controllerNamespace, webhookDeploymentName, timeout)

	// Then wait for VWC caBundle to be populated (self-signed cert injection).
	t.Log("Waiting for ValidatingWebhookConfiguration caBundle to be populated...")
	waitForCondition(t, timeout, defaultPollInterval, func() (bool, error) {
		vwc, err := clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(
			context.Background(), webhookConfigName, metav1.GetOptions{},
		)
		if err != nil {
			return false, fmt.Errorf("get VWC: %w", err)
		}
		if len(vwc.Webhooks) == 0 {
			return false, nil
		}
		caBundle := vwc.Webhooks[0].ClientConfig.CABundle
		if len(caBundle) == 0 {
			return false, nil
		}
		t.Logf("VWC caBundle populated (%d bytes)", len(caBundle))
		return true, nil
	})
}

// getValidatingWebhookConfig returns the ValidatingWebhookConfiguration for
// the nightjar webhook.
func getValidatingWebhookConfig(t *testing.T, clientset kubernetes.Interface) *admissionregistrationv1.ValidatingWebhookConfiguration {
	t.Helper()
	vwc, err := clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(
		context.Background(), webhookConfigName, metav1.GetOptions{},
	)
	require.NoError(t, err, "failed to get ValidatingWebhookConfiguration %s", webhookConfigName)
	return vwc
}

// getTLSSecret returns the webhook TLS Secret.
func getTLSSecret(t *testing.T, clientset kubernetes.Interface) *corev1.Secret {
	t.Helper()
	secret, err := clientset.CoreV1().Secrets(controllerNamespace).Get(
		context.Background(), webhookSecretName, metav1.GetOptions{},
	)
	require.NoError(t, err, "failed to get TLS secret %s/%s", controllerNamespace, webhookSecretName)
	return secret
}

// getWebhookLogs retrieves logs from the webhook pods for debugging.
func getWebhookLogs(t *testing.T, clientset kubernetes.Interface, tailLines int64) string {
	t.Helper()
	pods, err := clientset.CoreV1().Pods(controllerNamespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/component=webhook",
	})
	if err != nil {
		t.Logf("Warning: failed to list webhook pods: %v", err)
		return ""
	}
	if len(pods.Items) == 0 {
		t.Log("Warning: no webhook pods found")
		return ""
	}

	var allLogs strings.Builder
	for _, pod := range pods.Items {
		opts := &corev1.PodLogOptions{TailLines: &tailLines}
		stream, err := clientset.CoreV1().Pods(controllerNamespace).GetLogs(pod.Name, opts).Stream(context.Background())
		if err != nil {
			t.Logf("Warning: failed to get logs for webhook pod %s: %v", pod.Name, err)
			continue
		}
		var buf bytes.Buffer
		io.Copy(&buf, stream)
		stream.Close()
		allLogs.WriteString(fmt.Sprintf("=== %s ===\n%s\n", pod.Name, buf.String()))
	}
	return allLogs.String()
}

// getWebhookPDB returns the PodDisruptionBudget for the webhook, or nil if not found.
func getWebhookPDB(t *testing.T, clientset kubernetes.Interface) *policyv1.PodDisruptionBudget {
	t.Helper()
	pdb, err := clientset.PolicyV1().PodDisruptionBudgets(controllerNamespace).Get(
		context.Background(), webhookDeploymentName, metav1.GetOptions{},
	)
	if err != nil {
		return nil
	}
	return pdb
}

// scaleDeployment scales a deployment to the given replicas and waits for it to settle.
func scaleDeployment(t *testing.T, clientset kubernetes.Interface, namespace, name string, replicas int32) {
	t.Helper()
	deploy, err := clientset.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
	require.NoError(t, err, "get deployment %s/%s", namespace, name)

	deploy.Spec.Replicas = &replicas
	_, err = clientset.AppsV1().Deployments(namespace).Update(context.Background(), deploy, metav1.UpdateOptions{})
	require.NoError(t, err, "scale deployment %s/%s to %d", namespace, name, replicas)
	t.Logf("Scaled %s/%s to %d replicas", namespace, name, replicas)
}

// getWorkloadConstraints parses the nightjar.io/constraints JSON annotation
// from a Deployment and returns the decoded constraint summaries.
func getWorkloadConstraints(t *testing.T, dynamicClient dynamic.Interface, namespace, deploymentName string, timeout time.Duration) []constraintSummary {
	t.Helper()
	raw := waitForWorkloadAnnotation(t, dynamicClient, namespace, deploymentName,
		annotations.WorkloadConstraints, timeout)

	var summaries []constraintSummary
	require.NoError(t, json.Unmarshal([]byte(raw), &summaries),
		"failed to parse constraints JSON: %s", raw)
	return summaries
}
