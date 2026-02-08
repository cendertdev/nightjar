package servicemap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNew(t *testing.T) {
	sm := New(nil, zap.NewNop())
	require.NotNil(t, sm)
	assert.NotNil(t, sm.services)
	assert.NotNil(t, sm.ipToService)
	assert.NotNil(t, sm.portToServices)
	assert.NotNil(t, sm.endpointToService)
}

func TestUpsertService(t *testing.T) {
	sm := New(nil, zap.NewNop())

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prometheus",
			Namespace: "monitoring",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.0.0.100",
			Selector: map[string]string{
				"app": "prometheus",
			},
			Ports: []corev1.ServicePort{
				{
					Name:     "http",
					Port:     9090,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}

	sm.upsertService(svc)

	// Verify service was indexed
	assert.Equal(t, 1, sm.ServiceCount())

	// Verify GetService
	ports := sm.GetService("monitoring", "prometheus")
	require.Len(t, ports, 1)
	assert.Equal(t, "prometheus", ports[0].Name)
	assert.Equal(t, int32(9090), ports[0].Port)
	assert.Equal(t, "http", ports[0].PortName)

	// Verify ClusterIP lookup
	info := sm.ResolvePort("10.0.0.100", 9090)
	require.NotNil(t, info)
	assert.Equal(t, "prometheus", info.Name)
	assert.Equal(t, "monitoring", info.Namespace)

	// Verify port lookup in namespace
	services := sm.ResolvePortInNamespace("monitoring", 9090)
	require.Len(t, services, 1)
	assert.Equal(t, "prometheus", services[0].Name)
}

func TestDeleteService(t *testing.T) {
	sm := New(nil, zap.NewNop())

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prometheus",
			Namespace: "monitoring",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.0.0.100",
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 9090,
				},
			},
		},
	}

	sm.upsertService(svc)
	assert.Equal(t, 1, sm.ServiceCount())

	sm.deleteService("monitoring", "prometheus")
	assert.Equal(t, 0, sm.ServiceCount())

	// Verify indexes are cleaned up
	info := sm.ResolvePort("10.0.0.100", 9090)
	assert.Nil(t, info)

	services := sm.ResolvePortInNamespace("monitoring", 9090)
	assert.Empty(t, services)
}

func TestServicesForPod(t *testing.T) {
	sm := New(nil, zap.NewNop())

	// Service that selects app=backend
	svc1 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-service",
			Namespace: "production",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": "backend",
			},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
			},
		},
	}

	// Service that selects app=frontend
	svc2 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "frontend-service",
			Namespace: "production",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": "frontend",
			},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80},
			},
		},
	}

	sm.upsertService(svc1)
	sm.upsertService(svc2)

	// Pod with app=backend should match backend-service
	services := sm.ServicesForPod("production", map[string]string{"app": "backend", "version": "v1"})
	require.Len(t, services, 1)
	assert.Equal(t, "backend-service", services[0].Name)

	// Pod with app=frontend should match frontend-service
	services = sm.ServicesForPod("production", map[string]string{"app": "frontend"})
	require.Len(t, services, 1)
	assert.Equal(t, "frontend-service", services[0].Name)

	// Pod with different labels should not match any
	services = sm.ServicesForPod("production", map[string]string{"app": "other"})
	assert.Empty(t, services)

	// Different namespace should not match
	services = sm.ServicesForPod("staging", map[string]string{"app": "backend"})
	assert.Empty(t, services)
}

func TestUpsertEndpoints(t *testing.T) {
	sm := New(nil, zap.NewNop())

	// First create the service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "production",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.0.0.50",
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
			},
		},
	}
	sm.upsertService(svc)

	// Then create endpoints
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "production",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.1.10"},
					{IP: "10.0.1.11"},
				},
				Ports: []corev1.EndpointPort{
					{Name: "http", Port: 8080},
				},
			},
		},
	}
	sm.upsertEndpoints(ep)

	// Should be able to resolve endpoint IPs
	info := sm.ResolvePort("10.0.1.10", 8080)
	require.NotNil(t, info)
	assert.Equal(t, "backend", info.Name)

	info = sm.ResolvePort("10.0.1.11", 8080)
	require.NotNil(t, info)
	assert.Equal(t, "backend", info.Name)
}

func TestResolvePort_NotFound(t *testing.T) {
	sm := New(nil, zap.NewNop())

	// No services registered
	info := sm.ResolvePort("10.0.0.100", 9090)
	assert.Nil(t, info)
}

func TestMultipleServicesOnSamePort(t *testing.T) {
	sm := New(nil, zap.NewNop())

	// Two services on the same port
	svc1 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-a",
			Namespace: "production",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.0.0.10",
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}
	svc2 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-b",
			Namespace: "production",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.0.0.20",
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}

	sm.upsertService(svc1)
	sm.upsertService(svc2)

	// Both should be returned for port lookup
	services := sm.ResolvePortInNamespace("production", 8080)
	require.Len(t, services, 2)

	names := []string{services[0].Name, services[1].Name}
	assert.Contains(t, names, "service-a")
	assert.Contains(t, names, "service-b")

	// But ClusterIP lookup should be specific
	info := sm.ResolvePort("10.0.0.10", 8080)
	require.NotNil(t, info)
	assert.Equal(t, "service-a", info.Name)

	info = sm.ResolvePort("10.0.0.20", 8080)
	require.NotNil(t, info)
	assert.Equal(t, "service-b", info.Name)
}

func TestHeadlessService(t *testing.T) {
	sm := New(nil, zap.NewNop())

	// Headless service (ClusterIP = None)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "headless",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Ports: []corev1.ServicePort{
				{Port: 80},
			},
		},
	}
	sm.upsertService(svc)

	// Should not be in IP index
	info := sm.ResolvePort("None", 80)
	assert.Nil(t, info)

	// But should still be tracked
	assert.Equal(t, 1, sm.ServiceCount())
}

func TestFormatIPPort(t *testing.T) {
	tests := []struct {
		ip       string
		port     int32
		expected string
	}{
		{"10.0.0.1", 80, "10.0.0.1:80"},
		{"192.168.1.100", 8080, "192.168.1.100:8080"},
		{"::1", 443, "::1:443"},
	}

	for _, tt := range tests {
		result := formatIPPort(tt.ip, tt.port)
		assert.Equal(t, tt.expected, result)
	}
}
