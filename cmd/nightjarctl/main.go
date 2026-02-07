// nightjarctl is a CLI providing structured access to Nightjar data.
// Outputs JSON for agent consumption or human-readable tables for developers.
//
// # Commands
//
//	nightjarctl query -n <namespace> [-o json|table]
//	  List all constraints affecting a namespace.
//	  JSON output matches MCP QueryResult schema exactly.
//
//	nightjarctl explain "<error message>" -n <namespace> [-o json|table]
//	  Explain which constraint caused an error.
//	  JSON output matches MCP ExplainResult schema exactly.
//
//	nightjarctl check -f <manifest.yaml> [-o json|table]
//	  Pre-check if a manifest would be blocked.
//	  JSON output matches MCP CheckResult schema exactly.
//
//	nightjarctl remediate <constraint-name> -n <namespace> [-o json|table] [--dry-run]
//	  Get remediation steps for a constraint.
//	  JSON output matches MCP RemediationResult schema exactly.
//
//	nightjarctl status [-o json|table]
//	  Show Nightjar operational status.
//	  JSON output matches MCP HealthResponse schema exactly.
//
// # Output Formats
//
// -o json: Structured JSON matching MCP tool response schemas.
//
//	Agents should use this format.
//
// -o table: Human-readable table (default).
//
//	Developers use this interactively.
//
// # Implementation
//
// The CLI reads ConstraintReport CRDs and structured Event annotations.
// It does NOT require the MCP server to be running — it works directly
// against the Kubernetes API. This ensures agents that can run nightjarctl
// but can't connect to MCP still get structured output.
//
// The JSON output schemas are intentionally identical to MCP tool responses
// so agents can use either interface with the same parsing code.
package main

import (
	"fmt"
	"os"
)

func main() {
	// TODO: Implement nightjarctl CLI
	fmt.Fprintln(os.Stderr, "nightjarctl: not yet implemented")
	os.Exit(1)
}
