# Nightjar

**Automatic constraint discovery and developer notification for Kubernetes.**

Nightjar is a Kubernetes operator that automatically discovers all policies, constraints, quotas, and requirements across your cluster — regardless of which policy engine created them — and notifies developers when those constraints are blocking their workloads.

## The Problem

Modern Kubernetes clusters enforce constraints through many independent systems: Cilium network policies, Gatekeeper/OPA constraints, Kyverno policies, Istio authorization policies, native NetworkPolicies, ResourceQuotas, ValidatingWebhookConfigurations, and more. When a developer's deployment fails or their traffic is silently dropped, they have no unified way to discover *what* is blocking them or *why*. They spend hours debugging across scattered tools, kubectl commands, and Slack messages to platform teams.

## What This Does

Nightjar sits in your cluster and:

1. **Discovers** all constraint-like resources automatically by scanning CRDs, webhooks, network policies, quotas, and mesh configurations
2. **Indexes** them into a normalized model regardless of source engine
3. **Correlates** failures (admission rejections, traffic drops, missing prerequisites) to the specific constraint causing them
4. **Notifies** developers via Kubernetes Events, a `ConstraintReport` CRD, and optional external channels (Slack, webhooks) — with privacy-aware detail levels that don't leak cross-namespace security information

## Key Differentiators

- **Cross-engine**: Not tied to any single policy engine. Discovers constraints from Cilium, Gatekeeper, Kyverno, Istio, native K8s, and arbitrary CRDs.
- **Automatic discovery**: No manual registration of policy types. Scans the cluster's CRDs and heuristically identifies constraint-like resources, with pluggable adapters for deep parsing of known types.
- **Runtime awareness**: Integrates with Cilium Hubble for real-time traffic drop detection — developers are notified within seconds when a network policy blocks their traffic.
- **Missing resource detection**: Infers when a workload is missing a required companion resource (ServiceMonitor, VirtualService, PeerAuthentication, etc.) and alerts proactively.
- **Privacy-first**: Notifications are scoped by role. Developers see actionable summaries without cross-namespace policy details. Platform admins see full constraint specifics.
- **Developer experience focused**: The goal is not enforcement (policy engines already do that) but *explanation* — helping developers understand and resolve constraint issues quickly.

## Architecture

See [ARCHITECTURE.md](docs/ARCHITECTURE.md) for detailed architecture documentation.

```
┌─────────────────────────────────────────────────┐
│                Nightjar               │
│                                                  │
│  ┌─────────────┐  ┌──────────────┐              │
│  │  Discovery   │  │   Adapter    │              │
│  │   Engine     │──│   Registry   │              │
│  │             │  │              │              │
│  │ CRD Scanner │  │ Cilium       │              │
│  │ Heuristic   │  │ Gatekeeper   │              │
│  │ Detection   │  │ Kyverno      │              │
│  └──────┬──────┘  │ Istio        │              │
│         │         │ NetworkPolicy│              │
│         ▼         │ Webhooks     │              │
│  ┌─────────────┐  │ ResourceQuota│              │
│  │  Constraint  │  │ Generic      │              │
│  │   Indexer    │◄─┘              │              │
│  └──────┬──────┘                  │              │
│         │                         │              │
│         ▼                         │              │
│  ┌─────────────┐  ┌──────────────┐              │
│  │ Correlation  │  │  Requirement │              │
│  │   Engine     │  │  Evaluator   │              │
│  │             │  │              │              │
│  │ Events      │  │ Missing CRDs │              │
│  │ Hubble Flows│  │ Missing Mesh │              │
│  │ Dry-Run     │  │ Co-occurrence│              │
│  └──────┬──────┘  └──────┬───────┘              │
│         │                │                       │
│         ▼                ▼                       │
│  ┌─────────────────────────────┐                │
│  │   Notification Dispatcher   │                │
│  │                             │                │
│  │ K8s Events │ ConstraintReport │ Slack/Webhook│
│  └─────────────────────────────┘                │
└─────────────────────────────────────────────────┘

Separate Deployment:
┌─────────────────────────────────────────────────┐
│         Admission Webhook (Warning Mode)         │
│  Deploy-time warnings via K8s admission warnings │
│  failurePolicy: Ignore (always fail-open)        │
└─────────────────────────────────────────────────┘
```

## Quick Start

```bash
helm repo add nightjar https://yourorg.github.io/nightjar
helm install nightjar nightjar/nightjar \
  --namespace nightjar-system \
  --create-namespace
```

Then in any namespace:
```bash
kubectl get constraintreports -n my-namespace
```

## Project Status

🚧 **Pre-alpha / Planning** — See [PROJECT_PLAN.md](docs/PROJECT_PLAN.md) for the phased delivery roadmap.

## Agent & Automation Interfaces

Nightjar is designed for consumption by AI agents and automation tools, not just human developers. Every output has a structured, machine-parseable form.

| Interface | Who uses it | How |
|---|---|---|
| **MCP Server** | AI agents (Claude, Copilot, SRE bots) | Query constraints, explain errors, pre-check deploys, get remediation |
| **ConstraintReport CRD** | kubectl, agents, dashboards | `kubectl get constraintreport -n my-ns -o json` — includes `machineReadable` section |
| **Structured Events** | Any K8s event consumer | Events carry `nightjar.io/structured-data` JSON annotation |
| **Workload Annotations** | Agents inspecting Deployments | `nightjar.io/constraints` JSON annotation on affected workloads |
| **kubectl plugin** | CLI agents, scripts | `kubectl sentinel query -n my-ns -o json` — matches MCP response schemas |
| **Prometheus Metrics** | Monitoring agents, alertmanager | Per-namespace, per-workload constraint counts and quota utilization |
| **Capabilities API** | Agent discovery | `GET /api/v1/capabilities` — what adapters/features are active in this cluster |

See [Agent Outputs](docs/AGENT_OUTPUTS.md) for full design documentation.

## Documentation

- [Architecture](docs/ARCHITECTURE.md) — Deployment model, component design, data flow
- [Agent Outputs](docs/AGENT_OUTPUTS.md) — MCP server, structured events, machine-readable CRDs, kubectl plugin
- [Project Plan](docs/PROJECT_PLAN.md) — Phased delivery timeline and milestones
- [Privacy Model](docs/PRIVACY_MODEL.md) — Information classification and notification scoping
- [Adapter Guide](docs/ADAPTER_GUIDE.md) — How to write a constraint adapter for a new policy engine
- [Contributing](CONTRIBUTING.md) — How to contribute to the project

## License

Apache License 2.0 — See [LICENSE](LICENSE).
