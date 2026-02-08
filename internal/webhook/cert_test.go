package webhook

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDefaultCertManagerConfig(t *testing.T) {
	config := DefaultCertManagerConfig("test-ns")

	assert.Equal(t, CertModeSelfSigned, config.Mode)
	assert.Equal(t, "test-ns", config.Namespace)
	assert.Equal(t, "nightjar-webhook", config.ServiceName)
	assert.Equal(t, DefaultSecretName, config.SecretName)
	assert.Equal(t, DefaultWebhookConfigName, config.WebhookConfigName)
}

func TestCertManager_EnsureCertificates_SelfSigned_CreateNew(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := zap.NewNop()

	config := CertManagerConfig{
		Mode:              CertModeSelfSigned,
		Namespace:         "nightjar-system",
		ServiceName:       "nightjar-webhook",
		SecretName:        "test-tls",
		WebhookConfigName: "test-webhook",
	}

	cm := NewCertManager(client, config, logger)
	ctx := context.Background()

	// First call should create the secret
	err := cm.EnsureCertificates(ctx)
	require.NoError(t, err)

	// Verify secret was created
	secret, err := client.CoreV1().Secrets(config.Namespace).Get(ctx, config.SecretName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.NotEmpty(t, secret.Data["ca.crt"])
	assert.NotEmpty(t, secret.Data["tls.crt"])
	assert.NotEmpty(t, secret.Data["tls.key"])
	assert.Equal(t, corev1.SecretTypeTLS, secret.Type)

	// Verify certificates are valid
	caCert, serverCert, serverKey := cm.GetCertificates()
	assert.NotEmpty(t, caCert)
	assert.NotEmpty(t, serverCert)
	assert.NotEmpty(t, serverKey)

	// Parse and verify server certificate
	block, _ := pem.Decode(serverCert)
	require.NotNil(t, block, "failed to decode server cert PEM")

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	// Check DNS names include the service name
	assert.Contains(t, cert.DNSNames, config.ServiceName)
	assert.Contains(t, cert.DNSNames, config.ServiceName+"."+config.Namespace+".svc")

	// Check validity period
	assert.True(t, cert.NotAfter.After(time.Now().Add(CertValidityDuration-time.Hour)))
}

func TestCertManager_EnsureCertificates_SelfSigned_UseExisting(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := zap.NewNop()

	config := CertManagerConfig{
		Mode:              CertModeSelfSigned,
		Namespace:         "nightjar-system",
		ServiceName:       "nightjar-webhook",
		SecretName:        "test-tls",
		WebhookConfigName: "test-webhook",
	}

	cm := NewCertManager(client, config, logger)
	ctx := context.Background()

	// Create initial certificates
	err := cm.EnsureCertificates(ctx)
	require.NoError(t, err)

	initialCert, _, _ := cm.GetCertificates()

	// Create a new CertManager and ensure again
	cm2 := NewCertManager(client, config, logger)
	err = cm2.EnsureCertificates(ctx)
	require.NoError(t, err)

	// Should reuse existing certificates
	cert2, _, _ := cm2.GetCertificates()
	assert.Equal(t, initialCert, cert2, "should reuse existing valid certificates")
}

func TestCertManager_EnsureCertificates_CertManager_SecretExists(t *testing.T) {
	ctx := context.Background()

	// Pre-create secret as if cert-manager created it
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tls",
			Namespace: "nightjar-system",
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"ca.crt":  []byte("fake-ca-cert"),
			"tls.crt": []byte("fake-server-cert"),
			"tls.key": []byte("fake-server-key"),
		},
	}

	client := fake.NewSimpleClientset(secret)
	logger := zap.NewNop()

	config := CertManagerConfig{
		Mode:              CertModeCertManager,
		Namespace:         "nightjar-system",
		ServiceName:       "nightjar-webhook",
		SecretName:        "test-tls",
		WebhookConfigName: "test-webhook",
	}

	cm := NewCertManager(client, config, logger)

	err := cm.EnsureCertificates(ctx)
	require.NoError(t, err)

	caCert, serverCert, serverKey := cm.GetCertificates()
	assert.Equal(t, []byte("fake-ca-cert"), caCert)
	assert.Equal(t, []byte("fake-server-cert"), serverCert)
	assert.Equal(t, []byte("fake-server-key"), serverKey)
}

func TestCertManager_EnsureCertificates_CertManager_SecretMissing(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := zap.NewNop()

	config := CertManagerConfig{
		Mode:              CertModeCertManager,
		Namespace:         "nightjar-system",
		ServiceName:       "nightjar-webhook",
		SecretName:        "test-tls",
		WebhookConfigName: "test-webhook",
	}

	cm := NewCertManager(client, config, logger)
	ctx := context.Background()

	err := cm.EnsureCertificates(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCertManager_UpdateWebhookCABundle(t *testing.T) {
	ctx := context.Background()

	// Create webhook configuration
	failurePolicy := admissionregistrationv1.Ignore
	sideEffects := admissionregistrationv1.SideEffectClassNone
	webhookConfig := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-webhook",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test.nightjar.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca"),
				},
				FailurePolicy:           &failurePolicy,
				SideEffects:             &sideEffects,
				AdmissionReviewVersions: []string{"v1"},
			},
		},
	}

	client := fake.NewSimpleClientset(webhookConfig)
	logger := zap.NewNop()

	config := CertManagerConfig{
		Mode:              CertModeSelfSigned,
		Namespace:         "nightjar-system",
		ServiceName:       "nightjar-webhook",
		SecretName:        "test-tls",
		WebhookConfigName: "test-webhook",
	}

	cm := NewCertManager(client, config, logger)

	// Generate certificates first
	err := cm.EnsureCertificates(ctx)
	require.NoError(t, err)

	// Update webhook CA bundle
	err = cm.UpdateWebhookCABundle(ctx)
	require.NoError(t, err)

	// Verify webhook was updated
	updated, err := client.AdmissionregistrationV1().
		ValidatingWebhookConfigurations().
		Get(ctx, config.WebhookConfigName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, cm.GetCABundle(), updated.Webhooks[0].ClientConfig.CABundle)
}

func TestCertManager_UpdateWebhookCABundle_WebhookNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := zap.NewNop()

	config := CertManagerConfig{
		Mode:              CertModeSelfSigned,
		Namespace:         "nightjar-system",
		ServiceName:       "nightjar-webhook",
		SecretName:        "test-tls",
		WebhookConfigName: "test-webhook",
	}

	cm := NewCertManager(client, config, logger)
	ctx := context.Background()

	// Generate certificates first
	err := cm.EnsureCertificates(ctx)
	require.NoError(t, err)

	// Should not error when webhook doesn't exist
	err = cm.UpdateWebhookCABundle(ctx)
	assert.NoError(t, err)
}

func TestCertManager_NeedsRotation(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := zap.NewNop()

	config := CertManagerConfig{
		Mode:              CertModeSelfSigned,
		Namespace:         "nightjar-system",
		ServiceName:       "nightjar-webhook",
		SecretName:        "test-tls",
		WebhookConfigName: "test-webhook",
	}

	cm := NewCertManager(client, config, logger)
	ctx := context.Background()

	// No secret exists - needs rotation
	needs, err := cm.NeedsRotation(ctx)
	require.NoError(t, err)
	assert.True(t, needs, "should need rotation when secret doesn't exist")

	// Create certificates
	err = cm.EnsureCertificates(ctx)
	require.NoError(t, err)

	// With valid certs - doesn't need rotation
	needs, err = cm.NeedsRotation(ctx)
	require.NoError(t, err)
	assert.False(t, needs, "should not need rotation with valid certs")
}

func TestCertManager_GenerateCA(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := zap.NewNop()

	config := DefaultCertManagerConfig("test")
	cm := NewCertManager(client, config, logger)

	certPEM, keyPEM, err := cm.generateCA()
	require.NoError(t, err)

	// Parse certificate
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.True(t, cert.IsCA)
	assert.Equal(t, "Nightjar Webhook CA", cert.Subject.CommonName)
	assert.Contains(t, cert.Subject.Organization, "Nightjar")

	// Parse key
	keyBlock, _ := pem.Decode(keyPEM)
	require.NotNil(t, keyBlock)

	_, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	require.NoError(t, err)
}

func TestCertManager_GenerateServerCert(t *testing.T) {
	client := fake.NewSimpleClientset()
	logger := zap.NewNop()

	config := CertManagerConfig{
		Mode:        CertModeSelfSigned,
		Namespace:   "test-ns",
		ServiceName: "my-webhook",
	}
	cm := NewCertManager(client, config, logger)

	// Generate CA first
	caCertPEM, caKeyPEM, err := cm.generateCA()
	require.NoError(t, err)

	// Generate server cert
	serverCertPEM, serverKeyPEM, err := cm.generateServerCert(caCertPEM, caKeyPEM)
	require.NoError(t, err)

	// Parse server certificate
	block, _ := pem.Decode(serverCertPEM)
	require.NotNil(t, block)

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.False(t, cert.IsCA)
	assert.Equal(t, config.ServiceName, cert.Subject.CommonName)

	// Check DNS SANs
	expectedDNS := []string{
		"my-webhook",
		"my-webhook.test-ns",
		"my-webhook.test-ns.svc",
		"my-webhook.test-ns.svc.cluster.local",
	}
	for _, dns := range expectedDNS {
		assert.Contains(t, cert.DNSNames, dns)
	}

	// Verify key can be parsed
	keyBlock, _ := pem.Decode(serverKeyPEM)
	require.NotNil(t, keyBlock)

	_, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	require.NoError(t, err)

	// Verify certificate is signed by CA
	caBlock, _ := pem.Decode(caCertPEM)
	caCert, _ := x509.ParseCertificate(caBlock.Bytes)

	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	_, err = cert.Verify(x509.VerifyOptions{
		Roots: roots,
	})
	require.NoError(t, err, "server cert should be verified by CA")
}

func TestCreateWebhookConfiguration(t *testing.T) {
	caBundle := []byte("test-ca-bundle")
	config := CreateWebhookConfiguration("nightjar-system", "nightjar-webhook", "nightjar-webhook", caBundle)

	assert.Equal(t, "nightjar-webhook", config.Name)
	require.Len(t, config.Webhooks, 1)

	webhook := config.Webhooks[0]
	assert.Equal(t, "constraint-warning.nightjar.io", webhook.Name)
	assert.Equal(t, caBundle, webhook.ClientConfig.CABundle)
	assert.Equal(t, "nightjar-system", webhook.ClientConfig.Service.Namespace)
	assert.Equal(t, "nightjar-webhook", webhook.ClientConfig.Service.Name)
	assert.Equal(t, "/validate", *webhook.ClientConfig.Service.Path)

	// Verify fail-open policy
	assert.Equal(t, admissionregistrationv1.Ignore, *webhook.FailurePolicy)

	// Verify timeout
	assert.Equal(t, int32(5), *webhook.TimeoutSeconds)
}
