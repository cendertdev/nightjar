//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
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

// deleteNamespace deletes a namespace and waits for it to be fully removed.
func deleteNamespace(t *testing.T, clientset kubernetes.Interface, name string, timeout time.Duration) {
	t.Helper()
	err := clientset.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{})
	if err != nil {
		t.Logf("Warning: failed to delete namespace %s: %v", name, err)
		return
	}

	waitForCondition(t, timeout, defaultPollInterval, func() (bool, error) {
		_, err := clientset.CoreV1().Namespaces().Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			// Namespace is gone.
			return true, nil
		}
		return false, nil
	})
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
	default:
		// Best-effort: lowercase + "s"
		return strings.ToLower(kind) + "s"
	}
}
