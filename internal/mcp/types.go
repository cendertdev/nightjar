package mcp

import (
	"github.com/nightjarctl/nightjar/internal/types"
)

// --- Tool: nightjar_query ---

type QueryParams struct {
	Namespace          string            `json:"namespace"`
	WorkloadName       string            `json:"workload_name,omitempty"`
	WorkloadLabels     map[string]string `json:"workload_labels,omitempty"`
	ConstraintType     string            `json:"constraint_type,omitempty"`
	Severity           string            `json:"severity,omitempty"`
	IncludeRemediation bool              `json:"include_remediation"`
}

type QueryResult struct {
	Namespace   string             `json:"namespace"`
	Constraints []ConstraintResult `json:"constraints"`
	Total       int                `json:"total"`
}

type ConstraintResult struct {
	Name              string                 `json:"name"`
	Namespace         string                 `json:"namespace,omitempty"`
	ConstraintType    string                 `json:"constraint_type"`
	Severity          string                 `json:"severity"`
	SourceKind        string                 `json:"source_kind"`
	SourceAPIVersion  string                 `json:"source_api_version"`
	Effect            string                 `json:"effect"`
	AffectedWorkloads []string               `json:"affected_workloads,omitempty"`
	Remediation       *RemediationResult     `json:"remediation,omitempty"`
	Metrics           map[string]MetricValue `json:"metrics,omitempty"`
	Tags              []string               `json:"tags,omitempty"`
	DetailLevel       string                 `json:"detail_level"`
	LastObserved      string                 `json:"last_observed"`
}

// --- Tool: nightjar_explain ---

type ExplainParams struct {
	ErrorMessage string `json:"error_message"`
	Namespace    string `json:"namespace"`
	WorkloadName string `json:"workload_name,omitempty"`
}

type ExplainResult struct {
	Explanation         string             `json:"explanation"`
	MatchingConstraints []ConstraintResult `json:"matching_constraints"`
	Confidence          string             `json:"confidence"` // high, medium, low
	RemediationSteps    []RemediationStep  `json:"remediation_steps,omitempty"`
}

// --- Tool: nightjar_check ---

type CheckParams struct {
	Manifest string `json:"manifest"` // YAML
}

type CheckResult struct {
	WouldBlock           bool               `json:"would_block"`
	BlockingConstraints  []ConstraintResult `json:"blocking_constraints,omitempty"`
	MissingPrerequisites []MissingResource  `json:"missing_prerequisites,omitempty"`
	Warnings             []string           `json:"warnings,omitempty"`
}

// --- Tool: nightjar_list_namespaces ---

type NamespaceSummary struct {
	Namespace     string `json:"namespace"`
	Total         int    `json:"total"`
	CriticalCount int    `json:"critical_count"`
	WarningCount  int    `json:"warning_count"`
	InfoCount     int    `json:"info_count"`
	TopConstraint string `json:"top_constraint,omitempty"` // highest severity constraint name
}

// --- Tool: nightjar_remediation ---

type RemediationParams struct {
	ConstraintName string `json:"constraint_name"`
	Namespace      string `json:"namespace"`
}

type RemediationResult struct {
	Summary string            `json:"summary"`
	Steps   []RemediationStep `json:"steps"`
}

type RemediationStep struct {
	Type              string `json:"type"` // manual, kubectl, annotation, yaml_patch, link
	Description       string `json:"description"`
	Command           string `json:"command,omitempty"`
	Patch             string `json:"patch,omitempty"`
	Template          string `json:"template,omitempty"`
	URL               string `json:"url,omitempty"`
	Contact           string `json:"contact,omitempty"`
	RequiresPrivilege string `json:"requires_privilege,omitempty"` // developer, namespace-admin, cluster-admin
	Automated         bool   `json:"automated"`
}

// --- Resource: nightjar://health ---

type HealthResponse struct {
	Status   string                   `json:"status"` // healthy, degraded, unhealthy
	Adapters map[string]AdapterHealth `json:"adapters"`
	Hubble   *HubbleHealth            `json:"hubble,omitempty"`
	MCP      MCPHealth                `json:"mcp"`
	Indexer  IndexerHealth            `json:"indexer"`
	LastScan string                   `json:"last_scan"`
}

type AdapterHealth struct {
	Enabled          bool   `json:"enabled"`
	WatchedResources int    `json:"watched_resources"`
	ErrorCount       int    `json:"error_count"`
	Reason           string `json:"reason,omitempty"` // why disabled (e.g., "CRDs not installed")
}

type HubbleHealth struct {
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	Address   string `json:"address,omitempty"`
}

type MCPHealth struct {
	Enabled   bool   `json:"enabled"`
	Transport string `json:"transport"`
	Port      int    `json:"port"`
}

type IndexerHealth struct {
	TotalConstraints          int `json:"total_constraints"`
	NamespacesWithConstraints int `json:"namespaces_with_constraints"`
}

// --- Shared types ---

type MissingResource struct {
	ExpectedKind       string             `json:"expected_kind"`
	ExpectedAPIVersion string             `json:"expected_api_version"`
	Reason             string             `json:"reason"`
	Severity           string             `json:"severity"`
	ForWorkload        string             `json:"for_workload"`
	Remediation        *RemediationResult `json:"remediation,omitempty"`
}

type MetricValue struct {
	Hard        string  `json:"hard"`
	Used        string  `json:"used"`
	Unit        string  `json:"unit"`
	PercentUsed float64 `json:"percent_used"`
}

// ToConstraintResult converts an internal Constraint to an MCP-friendly result.
// IMPLEMENT: Map all fields, apply privacy scoping based on detailLevel.
func ToConstraintResult(c types.Constraint, detailLevel types.DetailLevel) ConstraintResult {
	// IMPLEMENT
	return ConstraintResult{}
}
