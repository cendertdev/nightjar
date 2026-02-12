//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
)

// E2ESuite is the base test suite for end-to-end tests.
// It connects to the current kubeconfig cluster and validates that the
// Nightjar controller is running before executing tests.
type E2ESuite struct {
	suite.Suite
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	namespace     string
	cleanupNS     func()
}

// SetupSuite runs once before all tests. It loads the kubeconfig,
// creates Kubernetes clients, creates a test namespace, and waits
// for the controller to be ready.
func (s *E2ESuite) SetupSuite() {
	t := s.T()

	// Load kubeconfig — handles KUBECONFIG env, ~/.kube/config, and in-cluster.
	cfg, err := ctrl.GetConfig()
	require.NoError(t, err, "failed to load kubeconfig — ensure KUBECONFIG is set or ~/.kube/config exists")

	clientset, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err, "failed to create kubernetes clientset")
	s.clientset = clientset

	dynClient, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err, "failed to create dynamic client")
	s.dynamicClient = dynClient

	// Create a test namespace for this run.
	ns, cleanup := createTestNamespace(t, clientset)
	s.namespace = ns
	s.cleanupNS = cleanup

	// Wait for the controller to be ready (deployed by make e2e-setup).
	waitForControllerReady(t, clientset, 120*time.Second)
}

// TearDownSuite runs once after all tests. It deletes the test namespace.
func (s *E2ESuite) TearDownSuite() {
	if s.cleanupNS != nil {
		s.cleanupNS()
	}
}

// TestControllerHealthy verifies the controller deployment is running and has
// ready replicas. This is a basic smoke test.
func (s *E2ESuite) TestControllerHealthy() {
	t := s.T()

	deploy, err := s.clientset.AppsV1().Deployments(controllerNamespace).Get(
		context.Background(), controllerDeploymentName, metav1.GetOptions{},
	)
	require.NoError(t, err, "failed to get controller deployment")
	require.Greater(t, deploy.Status.ReadyReplicas, int32(0),
		"controller has no ready replicas")

	t.Logf("Controller is healthy: %d ready replicas", deploy.Status.ReadyReplicas)
}

// TestControllerLogs verifies that the controller produces logs (sanity check).
func (s *E2ESuite) TestControllerLogs() {
	t := s.T()

	logs := getControllerLogs(t, s.clientset, 10)
	require.NotEmpty(t, logs, "controller produced no logs")

	t.Logf("Controller log tail:\n%s", logs)
}

// TestNamespaceCreated verifies the test namespace was created with the E2E label.
func (s *E2ESuite) TestNamespaceCreated() {
	t := s.T()

	ns, err := s.clientset.CoreV1().Namespaces().Get(
		context.Background(), s.namespace, metav1.GetOptions{},
	)
	require.NoError(t, err, "failed to get test namespace")
	require.Equal(t, "true", ns.Labels[e2eLabel], "test namespace missing e2e label")
}

func TestE2ESuite(t *testing.T) {
	suite.Run(t, new(E2ESuite))
}
