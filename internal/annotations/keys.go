// Package annotations defines the structured annotation keys that Nightjar
// writes to Kubernetes Events and workload objects. These annotations make outputs
// machine-parseable for AI agents and automation tools.
//
// # Event Annotations
//
// Every Event created by Nightjar carries structured annotations
// alongside the human-readable message. Agents can filter and parse these
// without text extraction.
//
// # Workload Annotations
//
// Affected workloads (Deployments, StatefulSets, etc.) are annotated with
// constraint summaries so agents inspecting a workload get constraint context
// immediately without querying a separate CRD.
package annotations

// Event annotation keys.
// These are written to every Event created by Nightjar.
const (
	// ManagedBy identifies Events created by Nightjar.
	// Value: "nightjar"
	// Usage: kubectl get events -l nightjar.io/managed-by=nightjar
	ManagedBy = "nightjar.io/managed-by"

	// EventConstraintType is the constraint category.
	// Value: "NetworkIngress", "NetworkEgress", "Admission", "ResourceLimit", "MeshPolicy", "MissingResource"
	EventConstraintType = "nightjar.io/constraint-type"

	// EventConstraintName is the name of the constraint object.
	// Redacted to "redacted" in summary detail level for cross-namespace constraints.
	EventConstraintName = "nightjar.io/constraint-name"

	// EventConstraintNamespace is the namespace of the constraint object.
	// Omitted in summary detail level for cross-namespace constraints.
	EventConstraintNamespace = "nightjar.io/constraint-namespace"

	// EventSourceGVR is the GroupVersionResource of the source policy object.
	// Value: "networking.k8s.io/v1/networkpolicies"
	EventSourceGVR = "nightjar.io/source-gvr"

	// EventSeverity is the severity level.
	// Value: "Critical", "Warning", "Info"
	EventSeverity = "nightjar.io/severity"

	// EventEffect is the constraint's effect.
	// Value: "deny", "restrict", "warn", "audit", "limit"
	EventEffect = "nightjar.io/effect"

	// EventDetailLevel indicates the privacy scoping applied.
	// Value: "summary", "detailed", "full"
	EventDetailLevel = "nightjar.io/detail-level"

	// EventRemediationType is the primary remediation type.
	// Value: "manual", "kubectl", "annotation", "yaml_patch", "link"
	EventRemediationType = "nightjar.io/remediation-type"

	// EventRemediationContact is the contact for manual remediation.
	// Value: email or Slack channel
	EventRemediationContact = "nightjar.io/remediation-contact"

	// EventStructuredData is a JSON blob containing the full machine-readable
	// constraint data. This is the primary annotation for agent consumption.
	// Agents should prefer parsing this over individual annotations.
	EventStructuredData = "nightjar.io/structured-data"
)

// Event label keys.
// Labels enable efficient kubectl filtering (annotations are not filterable).
const (
	// LabelManagedBy enables `kubectl get events -l nightjar.io/managed-by=nightjar`
	LabelManagedBy = "nightjar.io/managed-by"

	// LabelSeverity enables `kubectl get events -l nightjar.io/severity=critical`
	// Value is lowercased: "critical", "warning", "info"
	LabelSeverity = "nightjar.io/severity"

	// LabelConstraintType enables `kubectl get events -l nightjar.io/constraint-type=network-egress`
	// Value is kebab-cased: "network-ingress", "network-egress", "admission", "resource-limit", etc.
	LabelConstraintType = "nightjar.io/constraint-type"
)

// Workload annotation keys.
// These are written to Deployments, StatefulSets, etc. that are affected by constraints.
const (
	// WorkloadStatus is a one-line summary of constraints affecting this workload.
	// Value: "3 constraints (1 critical, 2 warning)"
	WorkloadStatus = "nightjar.io/status"

	// WorkloadLastEvaluated is the timestamp of the last constraint evaluation.
	// Value: RFC3339 timestamp
	WorkloadLastEvaluated = "nightjar.io/last-evaluated"

	// WorkloadConstraints is a JSON array of constraint summaries.
	// Value: [{"type":"NetworkEgress","severity":"Warning","name":"restrict-egress","source":"NetworkPolicy"}]
	// Agents can parse this for immediate constraint context without querying ConstraintReport.
	WorkloadConstraints = "nightjar.io/constraints"

	// WorkloadMaxSeverity is the highest severity constraint affecting this workload.
	// Value: "critical", "warning", "info", "none"
	// Enables quick triage: `kubectl get deploy -l nightjar.io/max-severity=critical`
	WorkloadMaxSeverity = "nightjar.io/max-severity"

	// WorkloadCriticalCount is the number of Critical severity constraints.
	WorkloadCriticalCount = "nightjar.io/critical-count"

	// WorkloadWarningCount is the number of Warning severity constraints.
	WorkloadWarningCount = "nightjar.io/warning-count"

	// WorkloadInfoCount is the number of Info severity constraints.
	WorkloadInfoCount = "nightjar.io/info-count"
)

// Well-known annotation values.
const (
	ManagedByValue = "nightjar"
)
