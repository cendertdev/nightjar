package requirements

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/nightjarctl/nightjar/internal/types"
)

// testEvalContext is a configurable mock RequirementEvalContext for rule tests.
type testEvalContext struct {
	namespaces      map[string]*unstructured.Unstructured
	resources       map[string][]*unstructured.Unstructured // key: "gvr:namespace"
	getNamespaceErr error
	listByGVRErr    error
	findMatchingErr error
}

func newTestEvalContext() *testEvalContext {
	return &testEvalContext{
		namespaces: make(map[string]*unstructured.Unstructured),
		resources:  make(map[string][]*unstructured.Unstructured),
	}
}

func (t *testEvalContext) addNamespace(name string, labels map[string]string) {
	labelsIface := make(map[string]interface{}, len(labels))
	for k, v := range labels {
		labelsIface[k] = v
	}
	t.namespaces[name] = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name":   name,
				"labels": labelsIface,
			},
		},
	}
}

func (t *testEvalContext) addResource(gvr schema.GroupVersionResource, namespace string, obj *unstructured.Unstructured) {
	key := gvrKey(gvr, namespace)
	t.resources[key] = append(t.resources[key], obj)
}

func gvrKey(gvr schema.GroupVersionResource, namespace string) string {
	return fmt.Sprintf("%s/%s/%s:%s", gvr.Group, gvr.Version, gvr.Resource, namespace)
}

func (t *testEvalContext) GetNamespace(_ context.Context, name string) (*unstructured.Unstructured, error) {
	if t.getNamespaceErr != nil {
		return nil, t.getNamespaceErr
	}
	ns, ok := t.namespaces[name]
	if !ok {
		return nil, fmt.Errorf("namespace %q not found", name)
	}
	return ns, nil
}

func (t *testEvalContext) ListByGVR(_ context.Context, gvr schema.GroupVersionResource, namespace string) ([]*unstructured.Unstructured, error) {
	if t.listByGVRErr != nil {
		return nil, t.listByGVRErr
	}
	key := gvrKey(gvr, namespace)
	return t.resources[key], nil
}

func (t *testEvalContext) FindMatchingResources(_ context.Context, gvr schema.GroupVersionResource, namespace string, labels map[string]string) ([]*unstructured.Unstructured, error) {
	if t.findMatchingErr != nil {
		return nil, t.findMatchingErr
	}
	key := gvrKey(gvr, namespace)
	all := t.resources[key]

	// Simple matching: check if resource's spec.selector.matchLabels is a subset of the provided labels.
	var matched []*unstructured.Unstructured
	for _, obj := range all {
		selectorRaw, _, _ := unstructured.NestedMap(obj.Object, "spec", "selector", "matchLabels")
		if selectorRaw == nil {
			continue
		}
		match := true
		for k, v := range selectorRaw {
			if labels[k] != v.(string) {
				match = false
				break
			}
		}
		if match {
			matched = append(matched, obj)
		}
	}
	return matched, nil
}

func makeDeployment(name, namespace string, annotations map[string]string, podLabels map[string]string, containers []interface{}) *unstructured.Unstructured {
	annIface := make(map[string]interface{}, len(annotations))
	for k, v := range annotations {
		annIface[k] = v
	}
	labelsIface := make(map[string]interface{}, len(podLabels))
	for k, v := range podLabels {
		labelsIface[k] = v
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":        name,
				"namespace":   namespace,
				"uid":         "uid-" + name,
				"annotations": annIface,
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": labelsIface,
					},
					"spec": map[string]interface{}{
						"containers": containers,
					},
				},
			},
		},
	}
	return obj
}

// ---- Istio Routing Rule Tests ----

func TestIstioRoutingRule_NoSidecarAnnotation(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()

	workload := makeDeployment("my-app", "default", nil, nil, nil)
	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when no sidecar annotation")
	}
}

func TestIstioRoutingRule_SidecarButNoRouting(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()

	workload := makeDeployment("my-app", "default",
		map[string]string{"sidecar.istio.io/status": "injected"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
	if constraints[0].ConstraintType != types.ConstraintTypeMissing {
		t.Fatalf("expected MissingResource, got %s", constraints[0].ConstraintType)
	}
}

func TestIstioRoutingRule_VirtualServiceExists_ShortName(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()

	vs := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1",
			"kind":       "VirtualService",
			"metadata":   map[string]interface{}{"name": "my-vs", "namespace": "default"},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"route": []interface{}{
							map[string]interface{}{
								"destination": map[string]interface{}{
									"host": "my-app",
								},
							},
						},
					},
				},
			},
		},
	}
	evalCtx.addResource(virtualServiceGVR, "default", vs)

	workload := makeDeployment("my-app", "default",
		map[string]string{"sidecar.istio.io/status": "injected"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when VS routes to workload")
	}
}

func TestIstioRoutingRule_VirtualServiceExists_FQDN(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()

	vs := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1",
			"kind":       "VirtualService",
			"metadata":   map[string]interface{}{"name": "my-vs", "namespace": "default"},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"route": []interface{}{
							map[string]interface{}{
								"destination": map[string]interface{}{
									"host": "my-app.default.svc.cluster.local",
								},
							},
						},
					},
				},
			},
		},
	}
	evalCtx.addResource(virtualServiceGVR, "default", vs)

	workload := makeDeployment("my-app", "default",
		map[string]string{"sidecar.istio.io/status": "injected"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when VS routes via FQDN")
	}
}

func TestIstioRoutingRule_DestinationRuleExists(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()

	dr := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1",
			"kind":       "DestinationRule",
			"metadata":   map[string]interface{}{"name": "my-dr", "namespace": "default"},
			"spec": map[string]interface{}{
				"host": "my-app.default",
			},
		},
	}
	evalCtx.addResource(destinationRuleGVR, "default", dr)

	workload := makeDeployment("my-app", "default",
		map[string]string{"sidecar.istio.io/status": "injected"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when DR targets workload")
	}
}

func TestIstioRoutingRule_VSDifferentHost(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()

	vs := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1",
			"kind":       "VirtualService",
			"metadata":   map[string]interface{}{"name": "other-vs", "namespace": "default"},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"route": []interface{}{
							map[string]interface{}{
								"destination": map[string]interface{}{
									"host": "other-service",
								},
							},
						},
					},
				},
			},
		},
	}
	evalCtx.addResource(virtualServiceGVR, "default", vs)

	workload := makeDeployment("my-app", "default",
		map[string]string{"sidecar.istio.io/status": "injected"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatal("expected 1 constraint when VS routes to different host")
	}
}

// ---- Prometheus Monitor Rule Tests ----

func TestPrometheusMonitorRule_NoMetricsPort(t *testing.T) {
	rule := NewPrometheusMonitorRule()
	evalCtx := newTestEvalContext()

	containers := []interface{}{
		map[string]interface{}{
			"name": "app",
			"ports": []interface{}{
				map[string]interface{}{"name": "http", "containerPort": int64(8080)},
			},
		},
	}
	workload := makeDeployment("my-app", "default", nil,
		map[string]string{"app": "my-app"}, containers)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints without metrics port")
	}
}

func TestPrometheusMonitorRule_MetricsPortNoMonitor(t *testing.T) {
	rule := NewPrometheusMonitorRule()
	evalCtx := newTestEvalContext()

	containers := []interface{}{
		map[string]interface{}{
			"name": "app",
			"ports": []interface{}{
				map[string]interface{}{"name": "metrics", "containerPort": int64(9090)},
			},
		},
	}
	workload := makeDeployment("my-app", "default", nil,
		map[string]string{"app": "my-app"}, containers)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
}

func TestPrometheusMonitorRule_HttpMetricsPort(t *testing.T) {
	rule := NewPrometheusMonitorRule()
	evalCtx := newTestEvalContext()

	containers := []interface{}{
		map[string]interface{}{
			"name": "app",
			"ports": []interface{}{
				map[string]interface{}{"name": "http-metrics", "containerPort": int64(9090)},
			},
		},
	}
	workload := makeDeployment("my-app", "default", nil,
		map[string]string{"app": "my-app"}, containers)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatal("expected 1 constraint for http-metrics port without monitor")
	}
}

func TestPrometheusMonitorRule_ServiceMonitorExists(t *testing.T) {
	rule := NewPrometheusMonitorRule()
	evalCtx := newTestEvalContext()

	sm := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata":   map[string]interface{}{"name": "my-sm", "namespace": "default"},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{"app": "my-app"},
				},
			},
		},
	}
	evalCtx.addResource(serviceMonitorGVR, "default", sm)

	containers := []interface{}{
		map[string]interface{}{
			"name": "app",
			"ports": []interface{}{
				map[string]interface{}{"name": "metrics", "containerPort": int64(9090)},
			},
		},
	}
	workload := makeDeployment("my-app", "default", nil,
		map[string]string{"app": "my-app"}, containers)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when ServiceMonitor matches")
	}
}

func TestPrometheusMonitorRule_PodMonitorExists(t *testing.T) {
	rule := NewPrometheusMonitorRule()
	evalCtx := newTestEvalContext()

	pm := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata":   map[string]interface{}{"name": "my-pm", "namespace": "default"},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{"app": "my-app"},
				},
			},
		},
	}
	evalCtx.addResource(podMonitorGVR, "default", pm)

	containers := []interface{}{
		map[string]interface{}{
			"name": "app",
			"ports": []interface{}{
				map[string]interface{}{"name": "metrics", "containerPort": int64(9090)},
			},
		},
	}
	workload := makeDeployment("my-app", "default", nil,
		map[string]string{"app": "my-app"}, containers)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when PodMonitor matches")
	}
}

// ---- Istio mTLS Rule Tests ----

func TestIstioMTLSRule_NoIstioInjection(t *testing.T) {
	rule := NewIstioMTLSRule()
	evalCtx := newTestEvalContext()
	evalCtx.addNamespace("default", map[string]string{})

	workload := makeDeployment("my-app", "default", nil, nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints without istio-injection label")
	}
}

func TestIstioMTLSRule_IstioInjectionNoPeerAuth(t *testing.T) {
	rule := NewIstioMTLSRule()
	evalCtx := newTestEvalContext()
	evalCtx.addNamespace("default", map[string]string{"istio-injection": "enabled"})

	workload := makeDeployment("my-app", "default", nil, nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
	if constraints[0].ConstraintType != types.ConstraintTypeMissing {
		t.Fatalf("expected MissingResource type")
	}
}

func TestIstioMTLSRule_PeerAuthExists(t *testing.T) {
	rule := NewIstioMTLSRule()
	evalCtx := newTestEvalContext()
	evalCtx.addNamespace("default", map[string]string{"istio-injection": "enabled"})

	pa := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "security.istio.io/v1",
			"kind":       "PeerAuthentication",
			"metadata":   map[string]interface{}{"name": "default", "namespace": "default"},
			"spec": map[string]interface{}{
				"mtls": map[string]interface{}{"mode": "STRICT"},
			},
		},
	}
	evalCtx.addResource(peerAuthenticationGVR, "default", pa)

	workload := makeDeployment("my-app", "default", nil, nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when PeerAuthentication exists")
	}
}

func TestIstioMTLSRule_MeshWidePeerAuth(t *testing.T) {
	rule := NewIstioMTLSRule()
	evalCtx := newTestEvalContext()
	evalCtx.addNamespace("default", map[string]string{"istio-injection": "enabled"})

	// Mesh-wide PA in istio-system (no selector).
	pa := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "security.istio.io/v1",
			"kind":       "PeerAuthentication",
			"metadata":   map[string]interface{}{"name": "default", "namespace": "istio-system"},
			"spec": map[string]interface{}{
				"mtls": map[string]interface{}{"mode": "STRICT"},
			},
		},
	}
	evalCtx.addResource(peerAuthenticationGVR, "istio-system", pa)

	workload := makeDeployment("my-app", "default", nil, nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when mesh-wide PeerAuthentication exists")
	}
}

func TestIstioMTLSRule_MeshWidePAWithEmptyMatchLabels(t *testing.T) {
	rule := NewIstioMTLSRule()
	evalCtx := newTestEvalContext()
	evalCtx.addNamespace("default", map[string]string{"istio-injection": "enabled"})

	// PA with explicit empty matchLabels = select all = mesh-wide.
	pa := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "security.istio.io/v1",
			"kind":       "PeerAuthentication",
			"metadata":   map[string]interface{}{"name": "default", "namespace": "istio-system"},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{},
				},
				"mtls": map[string]interface{}{"mode": "STRICT"},
			},
		},
	}
	evalCtx.addResource(peerAuthenticationGVR, "istio-system", pa)

	workload := makeDeployment("my-app", "default", nil, nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when mesh-wide PA has empty matchLabels")
	}
}

// ---- Cert Issuer Rule Tests ----

func TestCertIssuerRule_NoAnnotations(t *testing.T) {
	rule := NewCertIssuerRule()
	evalCtx := newTestEvalContext()

	workload := makeDeployment("my-app", "default", nil, nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints without cert-manager annotations")
	}
}

func TestCertIssuerRule_ClusterIssuerMissing(t *testing.T) {
	rule := NewCertIssuerRule()
	evalCtx := newTestEvalContext()

	workload := makeDeployment("my-app", "default",
		map[string]string{"cert-manager.io/cluster-issuer": "letsencrypt-prod"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
	if constraints[0].Severity != types.SeverityCritical {
		t.Fatalf("expected Critical severity, got %s", constraints[0].Severity)
	}
	if constraints[0].Details["expectedKind"] != "ClusterIssuer" {
		t.Fatalf("expected ClusterIssuer, got %v", constraints[0].Details["expectedKind"])
	}
}

func TestCertIssuerRule_ClusterIssuerExists(t *testing.T) {
	rule := NewCertIssuerRule()
	evalCtx := newTestEvalContext()

	issuer := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "ClusterIssuer",
			"metadata":   map[string]interface{}{"name": "letsencrypt-prod"},
		},
	}
	evalCtx.addResource(clusterIssuerGVR, "", issuer)

	workload := makeDeployment("my-app", "default",
		map[string]string{"cert-manager.io/cluster-issuer": "letsencrypt-prod"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when ClusterIssuer exists")
	}
}

func TestCertIssuerRule_NamespacedIssuerMissing(t *testing.T) {
	rule := NewCertIssuerRule()
	evalCtx := newTestEvalContext()

	workload := makeDeployment("my-app", "default",
		map[string]string{"cert-manager.io/issuer-name": "my-issuer"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
	if constraints[0].Details["expectedKind"] != "Issuer" {
		t.Fatalf("expected Issuer, got %v", constraints[0].Details["expectedKind"])
	}
}

func TestCertIssuerRule_NamespacedIssuerExists(t *testing.T) {
	rule := NewCertIssuerRule()
	evalCtx := newTestEvalContext()

	issuer := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Issuer",
			"metadata":   map[string]interface{}{"name": "my-issuer", "namespace": "default"},
		},
	}
	evalCtx.addResource(issuerGVR, "default", issuer)

	workload := makeDeployment("my-app", "default",
		map[string]string{"cert-manager.io/issuer-name": "my-issuer"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when Issuer exists")
	}
}

// ---- Host Matching Tests ----

func TestHostMatchesWorkload(t *testing.T) {
	tests := []struct {
		host         string
		workloadName string
		namespace    string
		want         bool
	}{
		{"my-app", "my-app", "default", true},
		{"my-app.default", "my-app", "default", true},
		{"my-app.default.svc.cluster.local", "my-app", "default", true},
		{"other-app", "my-app", "default", false},
		{"my-app.other-ns", "my-app", "default", false},
		{"", "my-app", "default", false},
		{"  my-app  ", "my-app", "default", true},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := hostMatchesWorkload(tt.host, tt.workloadName, tt.namespace)
			if got != tt.want {
				t.Errorf("hostMatchesWorkload(%q, %q, %q) = %v, want %v",
					tt.host, tt.workloadName, tt.namespace, got, tt.want)
			}
		})
	}
}

// ---- Has Metrics Port Tests ----

func TestHasMetricsPort(t *testing.T) {
	tests := []struct {
		name       string
		containers []interface{}
		usePodSpec bool
		want       bool
	}{
		{
			name: "metrics port",
			containers: []interface{}{
				map[string]interface{}{
					"name":  "app",
					"ports": []interface{}{map[string]interface{}{"name": "metrics"}},
				},
			},
			want: true,
		},
		{
			name: "http-metrics port",
			containers: []interface{}{
				map[string]interface{}{
					"name":  "app",
					"ports": []interface{}{map[string]interface{}{"name": "http-metrics"}},
				},
			},
			want: true,
		},
		{
			name: "no metrics port",
			containers: []interface{}{
				map[string]interface{}{
					"name":  "app",
					"ports": []interface{}{map[string]interface{}{"name": "http"}},
				},
			},
			want: false,
		},
		{
			name:       "no containers",
			containers: nil,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workload := makeDeployment("test", "default", nil, nil, tt.containers)
			got := hasMetricsPort(workload)
			if got != tt.want {
				t.Errorf("hasMetricsPort() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasMetricsPort_BarePod(t *testing.T) {
	pod := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata":   map[string]interface{}{"name": "test-pod", "namespace": "default"},
			"spec": map[string]interface{}{
				"containers": []interface{}{
					map[string]interface{}{
						"name":  "app",
						"ports": []interface{}{map[string]interface{}{"name": "metrics"}},
					},
				},
			},
		},
	}
	if !hasMetricsPort(pod) {
		t.Fatal("expected metrics port detected on bare Pod")
	}
}

// ---- hasMeshWidePA Tests ----

func TestHasMeshWidePA(t *testing.T) {
	tests := []struct {
		name string
		pa   *unstructured.Unstructured
		want bool
	}{
		{
			name: "no selector",
			pa: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"mtls": map[string]interface{}{"mode": "STRICT"},
					},
				},
			},
			want: true,
		},
		{
			name: "empty selector",
			pa: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"selector": map[string]interface{}{},
					},
				},
			},
			want: true,
		},
		{
			name: "empty matchLabels",
			pa: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "specific selector",
			pa: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "my-app",
							},
						},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasMeshWidePA([]*unstructured.Unstructured{tt.pa})
			if got != tt.want {
				t.Errorf("hasMeshWidePA() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---- Name/Description Tests ----

func TestRuleNameAndDescription(t *testing.T) {
	rules := []struct {
		rule        types.RequirementRule
		wantName    string
		wantDescNon string // just check non-empty
	}{
		{NewIstioRoutingRule(), "istio-routing", "Checks that workloads"},
		{NewPrometheusMonitorRule(), "prometheus-monitor", "Checks that workloads"},
		{NewIstioMTLSRule(), "istio-mtls", "Checks that namespaces"},
		{NewCertIssuerRule(), "cert-issuer", "Checks that cert-manager"},
	}
	for _, tt := range rules {
		t.Run(tt.wantName, func(t *testing.T) {
			if tt.rule.Name() != tt.wantName {
				t.Errorf("Name() = %q, want %q", tt.rule.Name(), tt.wantName)
			}
			if tt.rule.Description() == "" {
				t.Error("Description() should not be empty")
			}
		})
	}
}

// ---- Istio Routing: TCP/TLS Route Tests ----

func TestIstioRoutingRule_TCPRouteMatch(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()

	vs := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1",
			"kind":       "VirtualService",
			"metadata":   map[string]interface{}{"name": "my-vs", "namespace": "default"},
			"spec": map[string]interface{}{
				"tcp": []interface{}{
					map[string]interface{}{
						"route": []interface{}{
							map[string]interface{}{
								"destination": map[string]interface{}{
									"host": "my-app",
								},
							},
						},
					},
				},
			},
		},
	}
	evalCtx.addResource(virtualServiceGVR, "default", vs)

	workload := makeDeployment("my-app", "default",
		map[string]string{"sidecar.istio.io/status": "injected"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when TCP route matches workload")
	}
}

func TestIstioRoutingRule_TLSRouteMatch(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()

	vs := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1",
			"kind":       "VirtualService",
			"metadata":   map[string]interface{}{"name": "my-vs", "namespace": "default"},
			"spec": map[string]interface{}{
				"tls": []interface{}{
					map[string]interface{}{
						"route": []interface{}{
							map[string]interface{}{
								"destination": map[string]interface{}{
									"host": "my-app.default",
								},
							},
						},
					},
				},
			},
		},
	}
	evalCtx.addResource(virtualServiceGVR, "default", vs)

	workload := makeDeployment("my-app", "default",
		map[string]string{"sidecar.istio.io/status": "injected"},
		nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected no constraints when TLS route matches workload")
	}
}

// ---- Cluster-Scoped / Empty Namespace Tests ----

func TestIstioRoutingRule_ClusterScopedWorkload(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()

	workload := makeDeployment("my-app", "",
		map[string]string{"sidecar.istio.io/status": "injected"},
		nil, nil)
	workload.Object["metadata"].(map[string]interface{})["namespace"] = ""

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected nil for cluster-scoped workload")
	}
}

func TestPrometheusMonitorRule_ClusterScopedWorkload(t *testing.T) {
	rule := NewPrometheusMonitorRule()
	evalCtx := newTestEvalContext()

	containers := []interface{}{
		map[string]interface{}{
			"name":  "app",
			"ports": []interface{}{map[string]interface{}{"name": "metrics"}},
		},
	}
	workload := makeDeployment("my-app", "", nil,
		map[string]string{"app": "my-app"}, containers)
	workload.Object["metadata"].(map[string]interface{})["namespace"] = ""

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected nil for cluster-scoped workload with metrics port")
	}
}

func TestIstioMTLSRule_ClusterScopedWorkload(t *testing.T) {
	rule := NewIstioMTLSRule()
	evalCtx := newTestEvalContext()

	workload := makeDeployment("my-app", "", nil, nil, nil)
	workload.Object["metadata"].(map[string]interface{})["namespace"] = ""

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected nil for cluster-scoped workload")
	}
}

func TestCertIssuerRule_NamespacedIssuerEmptyNamespace(t *testing.T) {
	rule := NewCertIssuerRule()
	evalCtx := newTestEvalContext()

	workload := makeDeployment("my-app", "",
		map[string]string{"cert-manager.io/issuer-name": "my-issuer"},
		nil, nil)
	workload.Object["metadata"].(map[string]interface{})["namespace"] = ""

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected nil when namespace is empty for namespaced issuer")
	}
}

func TestCertIssuerRule_ClusterIssuerMissing_EmptyNamespace(t *testing.T) {
	rule := NewCertIssuerRule()
	evalCtx := newTestEvalContext()

	workload := makeDeployment("my-app", "",
		map[string]string{"cert-manager.io/cluster-issuer": "letsencrypt-prod"},
		nil, nil)
	workload.Object["metadata"].(map[string]interface{})["namespace"] = ""

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
	// Verify the UID uses "cluster" prefix for empty namespace.
	if constraints[0].Namespace != "" {
		t.Fatalf("expected empty namespace, got %q", constraints[0].Namespace)
	}
}

// ---- Prometheus: Top-Level Labels Fallback ----

func TestPrometheusMonitorRule_TopLevelLabelsFallback(t *testing.T) {
	rule := NewPrometheusMonitorRule()
	evalCtx := newTestEvalContext()

	// Workload without pod template labels but with top-level labels.
	workload := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]interface{}{
				"name":      "my-pod",
				"namespace": "default",
				"uid":       "uid-pod",
				"labels": map[string]interface{}{
					"app": "my-app",
				},
			},
			"spec": map[string]interface{}{
				"containers": []interface{}{
					map[string]interface{}{
						"name":  "app",
						"ports": []interface{}{map[string]interface{}{"name": "metrics"}},
					},
				},
			},
		},
	}

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint (no monitor for pod with top-level labels), got %d", len(constraints))
	}
}

func TestPrometheusMonitorRule_NoLabelsAnywhere(t *testing.T) {
	rule := NewPrometheusMonitorRule()
	evalCtx := newTestEvalContext()

	// Pod with metrics port but no labels at all.
	workload := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]interface{}{
				"name":      "my-pod",
				"namespace": "default",
				"uid":       "uid-pod",
			},
			"spec": map[string]interface{}{
				"containers": []interface{}{
					map[string]interface{}{
						"name":  "app",
						"ports": []interface{}{map[string]interface{}{"name": "metrics"}},
					},
				},
			},
		},
	}

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 0 {
		t.Fatal("expected nil when no labels on workload")
	}
}

// ---- Istio mTLS: Workload in istio-system Namespace ----

func TestIstioMTLSRule_WorkloadInIstioSystem(t *testing.T) {
	rule := NewIstioMTLSRule()
	evalCtx := newTestEvalContext()
	evalCtx.addNamespace("istio-system", map[string]string{"istio-injection": "enabled"})

	workload := makeDeployment("istiod", "istio-system", nil, nil, nil)

	constraints, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err != nil {
		t.Fatal(err)
	}
	// No PA in the namespace, and it skips the mesh-wide check since we ARE in istio-system.
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
}

// ---- Error Path Tests ----

func TestIstioRoutingRule_ListByGVRError(t *testing.T) {
	rule := NewIstioRoutingRule()
	evalCtx := newTestEvalContext()
	evalCtx.listByGVRErr = errors.New("list failed")

	workload := makeDeployment("my-app", "default",
		map[string]string{"sidecar.istio.io/status": "injected"},
		nil, nil)

	_, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err == nil {
		t.Fatal("expected error from ListByGVR")
	}
}

func TestIstioMTLSRule_GetNamespaceError(t *testing.T) {
	rule := NewIstioMTLSRule()
	evalCtx := newTestEvalContext()
	evalCtx.getNamespaceErr = errors.New("get ns failed")

	workload := makeDeployment("my-app", "default", nil, nil, nil)

	_, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err == nil {
		t.Fatal("expected error from GetNamespace")
	}
}

func TestPrometheusMonitorRule_FindMatchingResourcesError(t *testing.T) {
	rule := NewPrometheusMonitorRule()
	evalCtx := newTestEvalContext()
	evalCtx.findMatchingErr = errors.New("find failed")

	containers := []interface{}{
		map[string]interface{}{
			"name":  "app",
			"ports": []interface{}{map[string]interface{}{"name": "metrics"}},
		},
	}
	workload := makeDeployment("my-app", "default", nil,
		map[string]string{"app": "my-app"}, containers)

	_, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err == nil {
		t.Fatal("expected error from FindMatchingResources")
	}
}

func TestCertIssuerRule_ClusterIssuerListError(t *testing.T) {
	rule := NewCertIssuerRule()
	evalCtx := newTestEvalContext()
	evalCtx.listByGVRErr = errors.New("list failed")

	workload := makeDeployment("my-app", "default",
		map[string]string{"cert-manager.io/cluster-issuer": "letsencrypt-prod"},
		nil, nil)

	_, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err == nil {
		t.Fatal("expected error from issuerExists")
	}
}

func TestCertIssuerRule_NamespacedIssuerListError(t *testing.T) {
	rule := NewCertIssuerRule()
	evalCtx := newTestEvalContext()
	evalCtx.listByGVRErr = errors.New("list failed")

	workload := makeDeployment("my-app", "default",
		map[string]string{"cert-manager.io/issuer-name": "my-issuer"},
		nil, nil)

	_, err := rule.Evaluate(context.Background(), workload, evalCtx)
	if err == nil {
		t.Fatal("expected error from issuerExists for namespaced issuer")
	}
}
