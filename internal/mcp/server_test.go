package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/nightjarctl/nightjar/internal/indexer"
	"github.com/nightjarctl/nightjar/internal/types"
)

func setupTestServer() (*Server, *indexer.Indexer) {
	idx := indexer.New(nil)

	// Add some test constraints
	idx.Upsert(types.Constraint{
		UID:                k8stypes.UID("netpol-1"),
		Name:               "restrict-egress",
		Namespace:          "team-alpha",
		AffectedNamespaces: []string{"team-alpha"},
		ConstraintType:     types.ConstraintTypeNetworkEgress,
		Severity:           types.SeverityWarning,
		Effect:             "restrict",
		Summary:            "Restricts egress to port 443",
		Tags:               []string{"network", "egress"},
		Source:             schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	})

	idx.Upsert(types.Constraint{
		UID:                k8stypes.UID("quota-1"),
		Name:               "compute-quota",
		Namespace:          "team-alpha",
		AffectedNamespaces: []string{"team-alpha"},
		ConstraintType:     types.ConstraintTypeResourceLimit,
		Severity:           types.SeverityCritical,
		Effect:             "limit",
		Summary:            "CPU at 95%",
		Tags:               []string{"quota", "cpu"},
		Source:             schema.GroupVersionResource{Group: "", Version: "v1", Resource: "resourcequotas"},
		Details: map[string]interface{}{
			"resources": map[string]interface{}{
				"cpu": map[string]interface{}{
					"hard":    "4",
					"used":    "3.8",
					"percent": 95,
				},
			},
		},
	})

	idx.Upsert(types.Constraint{
		UID:                k8stypes.UID("webhook-1"),
		Name:               "pod-security",
		Namespace:          "",
		AffectedNamespaces: []string{"team-alpha", "team-beta"},
		ConstraintType:     types.ConstraintTypeAdmission,
		Severity:           types.SeverityInfo,
		Effect:             "intercept",
		Summary:            "Validates pod security",
		Tags:               []string{"admission", "security"},
		Source:             schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"},
	})

	opts := ServerOptions{
		Port:           8090,
		Transport:      "sse",
		Logger:         zap.NewNop(),
		DefaultContact: "platform@example.com",
		PrivacyResolver: func(r *http.Request) types.DetailLevel {
			return types.DetailLevelDetailed
		},
	}

	server := NewServer(idx, opts)
	return server, idx
}

func TestHandlers_Query(t *testing.T) {
	server, _ := setupTestServer()

	params := QueryParams{
		Namespace:          "team-alpha",
		IncludeRemediation: true,
	}

	body, _ := json.Marshal(params)
	req := httptest.NewRequest(http.MethodPost, "/tools/nightjar_query", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handlers.HandleQuery(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result QueryResult
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)

	assert.Equal(t, "team-alpha", result.Namespace)
	assert.Equal(t, 3, result.Total)
	assert.Len(t, result.Constraints, 3)

	// Should be sorted by severity (critical first)
	assert.Equal(t, "Critical", result.Constraints[0].Severity)
	assert.Equal(t, "Warning", result.Constraints[1].Severity)
	assert.Equal(t, "Info", result.Constraints[2].Severity)

	// Should have remediation
	assert.NotNil(t, result.Constraints[0].Remediation)
}

func TestHandlers_Query_WithFilters(t *testing.T) {
	server, _ := setupTestServer()

	params := QueryParams{
		Namespace:      "team-alpha",
		ConstraintType: "NetworkEgress",
	}

	body, _ := json.Marshal(params)
	req := httptest.NewRequest(http.MethodPost, "/tools/nightjar_query", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handlers.HandleQuery(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result QueryResult
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)

	assert.Equal(t, 1, result.Total)
	assert.Equal(t, "NetworkEgress", result.Constraints[0].ConstraintType)
}

func TestHandlers_Explain(t *testing.T) {
	server, _ := setupTestServer()

	params := ExplainParams{
		ErrorMessage: "connection timed out",
		Namespace:    "team-alpha",
	}

	body, _ := json.Marshal(params)
	req := httptest.NewRequest(http.MethodPost, "/tools/nightjar_explain", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handlers.HandleExplain(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result ExplainResult
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)

	assert.Equal(t, "high", result.Confidence)
	assert.Contains(t, result.Explanation, "network")
	assert.Len(t, result.MatchingConstraints, 1)
	assert.Equal(t, "NetworkEgress", result.MatchingConstraints[0].ConstraintType)
}

func TestHandlers_Explain_Quota(t *testing.T) {
	server, _ := setupTestServer()

	params := ExplainParams{
		ErrorMessage: "exceeded quota for resource cpu",
		Namespace:    "team-alpha",
	}

	body, _ := json.Marshal(params)
	req := httptest.NewRequest(http.MethodPost, "/tools/nightjar_explain", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handlers.HandleExplain(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result ExplainResult
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)

	assert.Equal(t, "high", result.Confidence)
	assert.Contains(t, result.Explanation, "quota")
	assert.Len(t, result.MatchingConstraints, 1)
	assert.Equal(t, "ResourceLimit", result.MatchingConstraints[0].ConstraintType)
}

func TestHandlers_Check(t *testing.T) {
	server, _ := setupTestServer()

	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-app
  namespace: team-alpha
  labels:
    app: test
spec:
  replicas: 1
`

	params := CheckParams{
		Manifest: manifest,
	}

	body, _ := json.Marshal(params)
	req := httptest.NewRequest(http.MethodPost, "/tools/nightjar_check", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handlers.HandleCheck(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result CheckResult
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)

	// No critical admission constraints, so should not block
	assert.False(t, result.WouldBlock)
}

func TestHandlers_ListNamespaces(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodPost, "/tools/nightjar_list_namespaces", nil)
	w := httptest.NewRecorder()

	server.handlers.HandleListNamespaces(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var summaries []NamespaceSummary
	err := json.NewDecoder(w.Body).Decode(&summaries)
	require.NoError(t, err)

	// Should have team-alpha and team-beta
	assert.GreaterOrEqual(t, len(summaries), 1)

	// Find team-alpha
	var teamAlpha *NamespaceSummary
	for i := range summaries {
		if summaries[i].Namespace == "team-alpha" {
			teamAlpha = &summaries[i]
			break
		}
	}

	require.NotNil(t, teamAlpha)
	assert.Equal(t, 3, teamAlpha.Total)
	assert.Equal(t, 1, teamAlpha.CriticalCount)
	assert.Equal(t, 1, teamAlpha.WarningCount)
	assert.Equal(t, 1, teamAlpha.InfoCount)
}

func TestHandlers_Remediation(t *testing.T) {
	server, _ := setupTestServer()

	params := RemediationParams{
		ConstraintName: "restrict-egress",
		Namespace:      "team-alpha",
	}

	body, _ := json.Marshal(params)
	req := httptest.NewRequest(http.MethodPost, "/tools/nightjar_remediation", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handlers.HandleRemediation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result RemediationResult
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)

	assert.NotEmpty(t, result.Summary)
	assert.NotEmpty(t, result.Steps)
}

func TestHandlers_Remediation_NotFound(t *testing.T) {
	server, _ := setupTestServer()

	params := RemediationParams{
		ConstraintName: "nonexistent",
		Namespace:      "team-alpha",
	}

	body, _ := json.Marshal(params)
	req := httptest.NewRequest(http.MethodPost, "/tools/nightjar_remediation", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handlers.HandleRemediation(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlers_ReportResource(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/resources/reports/team-alpha", nil)
	w := httptest.NewRecorder()

	server.handlers.HandleReportResource(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var report map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&report)
	require.NoError(t, err)

	assert.Equal(t, "team-alpha", report["namespace"])
	assert.Equal(t, float64(3), report["constraintCount"])
	assert.Equal(t, "1", report["schemaVersion"])
}

func TestHandlers_ConstraintResource(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/resources/constraints/team-alpha/restrict-egress", nil)
	w := httptest.NewRecorder()

	server.handlers.HandleConstraintResource(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result ConstraintResult
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)

	assert.Equal(t, "restrict-egress", result.Name)
	assert.Equal(t, "NetworkEgress", result.ConstraintType)
	assert.NotNil(t, result.Remediation)
}

func TestHandlers_HealthResource(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/resources/health", nil)
	w := httptest.NewRecorder()

	server.handlers.HandleHealthResource(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var health HealthResponse
	err := json.NewDecoder(w.Body).Decode(&health)
	require.NoError(t, err)

	assert.Equal(t, "healthy", health.Status)
	assert.True(t, health.MCP.Enabled)
	assert.Equal(t, 3, health.Indexer.TotalConstraints)
}

func TestHandlers_CapabilitiesResource(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/resources/capabilities", nil)
	w := httptest.NewRecorder()

	server.handlers.HandleCapabilitiesResource(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var caps map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&caps)
	require.NoError(t, err)

	assert.Equal(t, "1", caps["version"])
	assert.NotNil(t, caps["adapters"])
	assert.True(t, caps["mcpEnabled"].(bool))
}

func TestServer_ToolsList(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/mcp/tools", nil)
	w := httptest.NewRecorder()

	server.handleToolsList(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	tools := response["tools"].([]interface{})
	assert.Len(t, tools, 5)

	// Check tool names
	toolNames := make(map[string]bool)
	for _, t := range tools {
		tool := t.(map[string]interface{})
		toolNames[tool["name"].(string)] = true
	}

	assert.True(t, toolNames["nightjar_query"])
	assert.True(t, toolNames["nightjar_explain"])
	assert.True(t, toolNames["nightjar_check"])
	assert.True(t, toolNames["nightjar_list_namespaces"])
	assert.True(t, toolNames["nightjar_remediation"])
}

func TestServer_ResourcesList(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/mcp/resources", nil)
	w := httptest.NewRecorder()

	server.handleResourcesList(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	resources := response["resources"].([]interface{})
	assert.Len(t, resources, 4)
}

func TestToConstraintResult(t *testing.T) {
	c := types.Constraint{
		UID:            k8stypes.UID("test-uid"),
		Name:           "test-constraint",
		Namespace:      "test-ns",
		ConstraintType: types.ConstraintTypeNetworkEgress,
		Severity:       types.SeverityWarning,
		Effect:         "restrict",
		Tags:           []string{"network"},
		Source:         schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	}

	result := ToConstraintResult(c, types.DetailLevelDetailed, "test-ns")

	assert.Equal(t, "test-constraint", result.Name)
	assert.Equal(t, "test-ns", result.Namespace)
	assert.Equal(t, "NetworkEgress", result.ConstraintType)
	assert.Equal(t, "Warning", result.Severity)
	assert.Equal(t, "restrict", result.Effect)
	assert.Equal(t, "NetworkPolicy", result.SourceKind)
	assert.Equal(t, "networking.k8s.io/v1", result.SourceAPIVersion)
	assert.Equal(t, "detailed", result.DetailLevel)
}

func TestToConstraintResult_PrivacyScoping(t *testing.T) {
	c := types.Constraint{
		UID:            k8stypes.UID("cross-ns-uid"),
		Name:           "secret-policy",
		Namespace:      "kube-system",
		ConstraintType: types.ConstraintTypeNetworkEgress,
		Severity:       types.SeverityCritical,
		Source:         schema.GroupVersionResource{Resource: "networkpolicies"},
	}

	// Summary level - cross namespace should be redacted
	result := ToConstraintResult(c, types.DetailLevelSummary, "team-alpha")
	assert.Equal(t, "redacted", result.Name)
	assert.Empty(t, result.Namespace)

	// Detailed level - should show name but not cross-namespace info
	result = ToConstraintResult(c, types.DetailLevelDetailed, "team-alpha")
	assert.Equal(t, "secret-policy", result.Name)
	assert.Empty(t, result.Namespace) // Still hidden for cross-namespace

	// Full level - should show everything
	result = ToConstraintResult(c, types.DetailLevelFull, "team-alpha")
	assert.Equal(t, "secret-policy", result.Name)
	assert.Equal(t, "kube-system", result.Namespace)
}
