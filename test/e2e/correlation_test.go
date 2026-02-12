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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/nightjarctl/nightjar/internal/annotations"
)

// npGVR is the GroupVersionResource for NetworkPolicies.
var npGVR = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}

// TestCorrelationEventCreated verifies the full correlation pipeline:
// create a constraint (NetworkPolicy), create a Warning event,
// and verify that a Nightjar ConstraintNotification Event is emitted
// on the affected workload with structured annotations.
func (s *E2ESuite) TestCorrelationEventCreated() {
	t := s.T()
	ctx := context.Background()

	// 1. Create a default-deny-ingress NetworkPolicy.
	npName := "e2e-corr-deny-ingress"
	np := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "NetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      npName,
				"namespace": s.namespace,
				"labels": map[string]interface{}{
					e2eLabel: "true",
				},
			},
			"spec": map[string]interface{}{
				"podSelector": map[string]interface{}{},
				"policyTypes": []interface{}{"Ingress"},
			},
		},
	}
	_, err := s.dynamicClient.Resource(npGVR).Namespace(s.namespace).Create(ctx, np, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create NetworkPolicy")
	t.Cleanup(func() {
		_ = s.dynamicClient.Resource(npGVR).Namespace(s.namespace).Delete(ctx, npName, metav1.DeleteOptions{})
	})

	// 2. Wait for the constraint to be indexed (observable via ConstraintReport).
	waitForReportCondition(t, s.dynamicClient, s.namespace, reportCreateTimeout, func(status map[string]interface{}) bool {
		return statusInt64(status, "constraintCount") >= 1
	})
	t.Log("Constraint indexed (ConstraintReport appeared)")

	// 3. Create a test deployment.
	cleanupDep := createTestDeployment(t, s.dynamicClient, s.namespace, "corr-test-app")
	t.Cleanup(cleanupDep)

	// 4. Create a synthetic Warning event referencing the deployment.
	createWarningEvent(t, s.clientset, s.namespace, "corr-test-app", "Deployment")

	// 5. Wait for Nightjar to emit a ConstraintNotification event.
	events := waitForNightjarEvent(t, s.clientset, s.namespace, "corr-test-app", correlationEventTimeout)
	require.NotEmpty(t, events, "expected at least one Nightjar ConstraintNotification event")

	// 6. Verify the Event has structured annotations from EventBuilder.
	event := events[0]
	assert.Equal(t, "ConstraintNotification", event.Reason)
	assert.Equal(t, "nightjar-controller", event.Source.Component)

	// Verify structured annotations are present.
	require.NotNil(t, event.Annotations, "event should have annotations")
	assertManagedByNightjar(t, event)
	assert.NotEmpty(t, event.Annotations[annotations.EventConstraintType],
		"nightjar.io/constraint-type should be set")
	assert.NotEmpty(t, event.Annotations[annotations.EventSeverity],
		"nightjar.io/severity should be set")
	assert.NotEmpty(t, event.Annotations[annotations.EventDetailLevel],
		"nightjar.io/detail-level should be set")
	assert.NotEmpty(t, event.Annotations[annotations.EventSourceGVR],
		"nightjar.io/source-gvr should be set")

	// Verify labels for kubectl filtering.
	require.NotNil(t, event.Labels, "event should have labels")
	assert.Equal(t, annotations.ManagedByValue, event.Labels[annotations.LabelManagedBy])

	t.Logf("Correlation event created: %s (annotations=%d, labels=%d)",
		event.Name, len(event.Annotations), len(event.Labels))
}

// TestCorrelationDeduplication verifies that the Dispatcher suppresses duplicate
// notifications for the same constraint-workload pair within the suppression window.
//
// Strategy: Send warning events until the Nightjar event count for the workload
// stabilizes (all reachable constraints are dedup'd). Then send one more warning
// and verify no new events appear. This approach is resilient to environments with
// many cluster-scoped constraints where the dispatcher rate limiter causes
// incomplete coverage per individual warning event.
func (s *E2ESuite) TestCorrelationDeduplication() {
	t := s.T()
	ctx := context.Background()

	// 1. Create a NetworkPolicy.
	npName := "e2e-corr-dedup-np"
	np := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "NetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      npName,
				"namespace": s.namespace,
				"labels": map[string]interface{}{
					e2eLabel: "true",
				},
			},
			"spec": map[string]interface{}{
				"podSelector": map[string]interface{}{},
				"policyTypes": []interface{}{"Ingress"},
			},
		},
	}
	_, err := s.dynamicClient.Resource(npGVR).Namespace(s.namespace).Create(ctx, np, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create NetworkPolicy")
	t.Cleanup(func() {
		_ = s.dynamicClient.Resource(npGVR).Namespace(s.namespace).Delete(ctx, npName, metav1.DeleteOptions{})
	})

	// 2. Wait for the constraint to be indexed.
	waitForReportCondition(t, s.dynamicClient, s.namespace, reportCreateTimeout, func(status map[string]interface{}) bool {
		names := statusConstraintNames(status)
		for _, n := range names {
			if n == npName {
				return true
			}
		}
		return false
	})

	// 3. Create a deployment.
	cleanupDep := createTestDeployment(t, s.dynamicClient, s.namespace, "dedup-test-app")
	t.Cleanup(cleanupDep)

	// 4. Send warning events until the Nightjar event count stabilizes.
	// Each warning triggers the correlator for all ~N constraints; the dispatcher
	// rate limiter (burst=10, 1.67/sec) lets some through and dedup-marks them.
	// After enough warnings, all reachable constraints are dedup'd and count stabilizes.
	stabilizedCount := waitForStableNightjarEventCount(t, s.clientset, s.namespace, "dedup-test-app",
		120*time.Second)
	require.Greater(t, stabilizedCount, 0, "expected at least one Nightjar event before stabilization")
	t.Logf("Event count stabilized at %d", stabilizedCount)

	// 5. Send one more Warning event (different event UID, same workload).
	// The Dispatcher deduplicates on (constraintUID, workloadUID) with a 60-minute window.
	// All constraint-workload pairs are already marked, so no new events should appear.
	createWarningEvent(t, s.clientset, s.namespace, "dedup-test-app", "Deployment")

	// 6. Wait a few seconds for processing, then count Nightjar events.
	time.Sleep(5 * time.Second)
	finalEvents := getNightjarEvents(t, s.clientset, s.namespace, "dedup-test-app")
	t.Logf("After dedup warning: %d Nightjar events (stabilized was %d)", len(finalEvents), stabilizedCount)

	// The count should not have increased — the Dispatcher suppresses all duplicates.
	assert.Equal(t, stabilizedCount, len(finalEvents),
		"Dispatcher should deduplicate same constraint-workload pairs; expected %d, got %d",
		stabilizedCount, len(finalEvents))
}

// TestCorrelationPrivacyScoping verifies that Events for cross-namespace constraints
// use summary-level privacy scoping: no constraint name, no cross-namespace details.
func (s *E2ESuite) TestCorrelationPrivacyScoping() {
	t := s.T()
	ctx := context.Background()

	// 1. Create a cluster-scoped ValidatingWebhookConfiguration (cross-namespace).
	webhookName := fmt.Sprintf("e2e-corr-privacy-%s", s.namespace)
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
					"name":                    "e2e-privacy.example.io",
					"admissionReviewVersions": []interface{}{"v1"},
					"sideEffects":             "None",
					"failurePolicy":           "Ignore",
					"clientConfig": map[string]interface{}{
						"url": "https://localhost:9443/validate-privacy",
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
	_, err := s.dynamicClient.Resource(vwhcGVR).Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create ValidatingWebhookConfiguration")
	t.Cleanup(func() {
		_ = s.dynamicClient.Resource(vwhcGVR).Delete(ctx, webhookName, metav1.DeleteOptions{})
	})

	// 2. Wait for the constraint to be indexed in the test namespace.
	waitForReportCondition(t, s.dynamicClient, s.namespace, reportCreateTimeout, func(status map[string]interface{}) bool {
		sources := statusConstraintSources(status)
		for _, src := range sources {
			if src == "validatingwebhookconfigurations" {
				return true
			}
		}
		return false
	})
	t.Log("Cluster-scoped constraint indexed")

	// 3. Create a deployment.
	cleanupDep := createTestDeployment(t, s.dynamicClient, s.namespace, "privacy-test-app")
	t.Cleanup(cleanupDep)

	// 4. Wait for a Nightjar event with Admission constraint type.
	// The helper sends warning events and retries to handle the case where the
	// dispatcher rate-limiter drops the Admission notification on the first try.
	events := waitForNightjarEventByAnnotation(t, s.clientset, s.namespace, "privacy-test-app",
		annotations.EventConstraintType, "Admission", correlationEventTimeout)
	require.NotEmpty(t, events, "expected at least one Admission-type Nightjar event")

	// 5. Verify privacy scoping on the Admission event's annotations.
	webhookEvent := &events[0]
	require.NotNil(t, webhookEvent.Annotations, "event should have annotations")

	// At summary level for cross-namespace constraint:
	// - constraint-name should be "redacted"
	// - constraint-namespace should be absent (cluster-scoped has no namespace)
	// - detail-level should be "summary"
	assert.Equal(t, "redacted", webhookEvent.Annotations[annotations.EventConstraintName],
		"cross-namespace constraint name should be redacted at summary level")
	assert.Equal(t, "summary", webhookEvent.Annotations[annotations.EventDetailLevel],
		"developer events should use summary detail level")

	// The message should NOT contain the webhook name (privacy rule).
	assert.NotContains(t, webhookEvent.Message, webhookName,
		"summary message should not contain cross-namespace constraint name")

	t.Logf("Privacy scoping verified: name=%s, detail-level=%s",
		webhookEvent.Annotations[annotations.EventConstraintName],
		webhookEvent.Annotations[annotations.EventDetailLevel])
}

// TestWorkloadAnnotationPatched verifies that the WorkloadAnnotator patches
// Deployments with constraint metadata annotations when constraints exist.
func (s *E2ESuite) TestWorkloadAnnotationPatched() {
	t := s.T()
	ctx := context.Background()

	// 1. Create a NetworkPolicy.
	npName := "e2e-corr-annot-np"
	np := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "NetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      npName,
				"namespace": s.namespace,
				"labels": map[string]interface{}{
					e2eLabel: "true",
				},
			},
			"spec": map[string]interface{}{
				"podSelector": map[string]interface{}{},
				"policyTypes": []interface{}{"Ingress"},
			},
		},
	}
	_, err := s.dynamicClient.Resource(npGVR).Namespace(s.namespace).Create(ctx, np, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create NetworkPolicy")
	t.Cleanup(func() {
		_ = s.dynamicClient.Resource(npGVR).Namespace(s.namespace).Delete(ctx, npName, metav1.DeleteOptions{})
	})

	// 2. Create a deployment.
	cleanupDep := createTestDeployment(t, s.dynamicClient, s.namespace, "annot-test-app")
	t.Cleanup(cleanupDep)
	waitForDeploymentReady(t, s.clientset, s.namespace, "annot-test-app", defaultTimeout)

	// 3. Wait for the constraint to be indexed.
	waitForReportCondition(t, s.dynamicClient, s.namespace, reportCreateTimeout, func(status map[string]interface{}) bool {
		return statusInt64(status, "constraintCount") >= 1
	})

	// 4. Wait for the deployment to receive nightjar.io/status annotation.
	status := waitForWorkloadAnnotation(t, s.dynamicClient, s.namespace, "annot-test-app",
		annotations.WorkloadStatus, workloadAnnotationTimeout)
	assert.NotEmpty(t, status, "nightjar.io/status should be set")
	assert.Contains(t, status, "constraint", "status should mention constraints")
	t.Logf("Workload status: %s", status)

	// 5. Verify max-severity.
	depGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	dep, err := s.dynamicClient.Resource(depGVR).Namespace(s.namespace).Get(
		ctx, "annot-test-app", metav1.GetOptions{},
	)
	require.NoError(t, err)
	annots := dep.GetAnnotations()
	require.NotNil(t, annots)

	maxSeverity := annots[annotations.WorkloadMaxSeverity]
	assert.NotEmpty(t, maxSeverity, "nightjar.io/max-severity should be set")
	t.Logf("Max severity: %s", maxSeverity)

	// 6. Verify constraints JSON annotation.
	constraintsJSON := annots[annotations.WorkloadConstraints]
	assert.NotEmpty(t, constraintsJSON, "nightjar.io/constraints should be set")

	var summaries []map[string]interface{}
	err = json.Unmarshal([]byte(constraintsJSON), &summaries)
	require.NoError(t, err, "constraints annotation should be valid JSON")
	require.NotEmpty(t, summaries, "constraints list should not be empty")

	// Find the NetworkPolicy constraint.
	found := false
	for _, s := range summaries {
		source, _ := s["source"].(string)
		if source == "networkpolicies" {
			found = true
			ct, _ := s["type"].(string)
			assert.Contains(t, ct, "Network", "constraint type should be a network type")
			break
		}
	}
	assert.True(t, found, "constraints should include the NetworkPolicy; got: %s", constraintsJSON)

	// 7. Verify severity counts.
	assert.NotEmpty(t, annots[annotations.WorkloadWarningCount], "warning-count should be set")
	assert.NotEmpty(t, annots[annotations.WorkloadLastEvaluated], "last-evaluated should be set")

	t.Logf("Workload annotations verified: %d constraint summaries", len(summaries))
}

// TestCorrelationRateLimiting verifies that the Dispatcher's per-namespace rate limiter
// prevents excessive event creation under burst traffic.
func (s *E2ESuite) TestCorrelationRateLimiting() {
	t := s.T()
	ctx := context.Background()

	// 1. Create a NetworkPolicy to provide an indexed constraint.
	npName := "e2e-corr-ratelimit-np"
	np := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "NetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      npName,
				"namespace": s.namespace,
				"labels": map[string]interface{}{
					e2eLabel: "true",
				},
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
	_, err := s.dynamicClient.Resource(npGVR).Namespace(s.namespace).Create(ctx, np, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create NetworkPolicy")
	t.Cleanup(func() {
		_ = s.dynamicClient.Resource(npGVR).Namespace(s.namespace).Delete(ctx, npName, metav1.DeleteOptions{})
	})

	// 2. Wait for the constraint to be indexed.
	waitForReportCondition(t, s.dynamicClient, s.namespace, reportCreateTimeout, func(status map[string]interface{}) bool {
		names := statusConstraintNames(status)
		for _, n := range names {
			if n == npName {
				return true
			}
		}
		return false
	})

	// 3. Create many Warning events referencing DIFFERENT workloads.
	// Each unique workload avoids Dispatcher deduplication, so the rate limiter
	// is the primary throttling mechanism.
	burstCount := 50
	for i := 0; i < burstCount; i++ {
		workloadName := fmt.Sprintf("rl-workload-%03d", i)
		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "e2e-rl-",
				Namespace:    s.namespace,
				Labels: map[string]string{
					e2eLabel: "true",
				},
			},
			InvolvedObject: corev1.ObjectReference{
				Kind:      "Deployment",
				Namespace: s.namespace,
				Name:      workloadName,
			},
			Reason:         "E2ERateLimitTest",
			Message:        "Burst warning event for rate limit testing",
			Type:           "Warning",
			Source:         corev1.EventSource{Component: "e2e-test"},
			FirstTimestamp: metav1.Now(),
			LastTimestamp:  metav1.Now(),
			Count:          1,
		}
		_, err := s.clientset.CoreV1().Events(s.namespace).Create(ctx, event, metav1.CreateOptions{})
		if err != nil {
			t.Logf("Warning: failed to create event %d: %v", i, err)
		}
	}
	t.Logf("Created %d burst Warning events targeting different workloads", burstCount)

	// 4. Wait for some processing time.
	time.Sleep(10 * time.Second)

	// 5. Count total Nightjar ConstraintNotification events across all rl-workload-* names.
	allEvents, err := s.clientset.CoreV1().Events(s.namespace).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)

	nightjarCount := 0
	for _, ev := range allEvents.Items {
		if ev.Reason == "ConstraintNotification" &&
			ev.Source.Component == "nightjar-controller" &&
			len(ev.InvolvedObject.Name) > 3 && ev.InvolvedObject.Name[:3] == "rl-" {
			nightjarCount++
		}
	}
	t.Logf("Nightjar events created: %d out of %d burst events", nightjarCount, burstCount)

	// The Dispatcher rate limiter (100/minute = ~1.67/sec, burst = 10) should cap throughput.
	// We sent 50 events; if rate limiting works, significantly fewer than 50 Nightjar events
	// should be created. We allow some slack for timing.
	assert.Less(t, nightjarCount, burstCount,
		"Rate limiter should prevent all %d events from producing Nightjar events", burstCount)

	// At least some events should get through (the burst allowance).
	assert.Greater(t, nightjarCount, 0,
		"At least some events should pass through the rate limiter")
}
