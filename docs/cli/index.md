---
layout: default
title: CLI (nightjar)
nav_order: 3
has_children: true
permalink: /cli/
---

# CLI Reference
{: .no_toc }

The `nightjar` command-line tool queries constraints and explains errors.
{: .fs-6 .fw-300 }

---

## Installation

### Using Go

```bash
go install github.com/nightjarctl/nightjar/cmd/nightjarctl@latest
```

### From Source

```bash
git clone https://github.com/nightjarctl/nightjar.git
cd nightjar
make build
mv bin/nightjar /usr/local/bin/
```

### Verify

```bash
nightjar version
nightjar --help
```

---

## Commands Overview

| Command | Purpose |
|---------|---------|
| [query](query/) | Query constraints affecting a namespace |
| [explain](explain/) | Match an error message to constraints |
| [check](check/) | Pre-check a manifest before deploying |
| [remediate](remediate/) | Get remediation steps for a constraint |
| [status](status/) | Show cluster-wide constraint summary |

---

## Global Flags

All commands accept these flags:

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | `table` | Output format: `table`, `json`, `yaml` |
| `--help` | `-h` | | Show help for command |
| `--version` | | | Show version |

---

## Output Formats

### Table (Default)

Human-readable tabular output:

```bash
nightjar query -n my-namespace
```

```
NAMESPACE     NAME             TYPE           SEVERITY   EFFECT
my-namespace  restrict-egress  NetworkEgress  Critical   deny
my-namespace  compute-quota    ResourceLimit  Warning    limit
```

### JSON

Structured JSON matching MCP response schemas:

```bash
nightjar query -n my-namespace -o json
```

```json
{
  "namespace": "my-namespace",
  "constraints": [
    {
      "name": "restrict-egress",
      "constraint_type": "NetworkEgress",
      "severity": "Critical",
      "effect": "deny",
      "source_kind": "NetworkPolicy",
      "source_api_version": "networking.k8s.io/v1",
      "tags": ["network", "egress"],
      "detail_level": "summary",
      "last_observed": "2024-01-15T10:30:00Z"
    }
  ],
  "total": 1
}
```

### YAML

YAML output for readability:

```bash
nightjar query -n my-namespace -o yaml
```

```yaml
namespace: my-namespace
constraints:
  - name: restrict-egress
    constraint_type: NetworkEgress
    severity: Critical
    effect: deny
total: 1
```

---

## Data Source

The CLI reads data directly from ConstraintReport CRDs in the cluster. It does not require the Nightjar controller to be running, but the reports must have been created by the controller.

```bash
# The CLI reads from:
kubectl get constraintreport -n <namespace>
```

---

## RBAC Requirements

The CLI requires read access to ConstraintReport CRDs:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: nightjar-cli-user
rules:
  - apiGroups: ["nightjar.io"]
    resources: ["constraintreports"]
    verbs: ["get", "list"]
```

For namespace-scoped access, use a Role instead of ClusterRole.

---

## kubectl Plugin Alternative

The CLI can also be invoked as a kubectl plugin:

```bash
# Install
go install github.com/nightjarctl/nightjar/cmd/kubectl-sentinel@latest

# Use (identical commands)
kubectl sentinel query -n my-namespace
kubectl sentinel explain -n my-namespace "connection refused"
kubectl sentinel check -f deployment.yaml
```

The kubectl plugin shares the same codebase and accepts the same flags.
