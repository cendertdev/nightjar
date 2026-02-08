---
layout: default
title: Configuration
parent: Controller
nav_order: 1
---

# Configuration Reference
{: .no_toc }

Complete Helm values reference for Nightjar.
{: .fs-6 .fw-300 }

## Table of contents
{: .no_toc .text-delta }

1. TOC
{:toc}

---

## Controller

```yaml
controller:
  # Number of replicas (use 2 for HA)
  replicas: 2

  image:
    repository: ghcr.io/nightjarctl/nightjar
    tag: ""  # Defaults to Chart appVersion
    pullPolicy: IfNotPresent

  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: 500m
      memory: 512Mi

  # Enable leader election for HA deployments
  leaderElect: true

  # How often to scan for newly installed CRDs
  rescanInterval: 5m

  # Additional arguments to pass to the controller binary
  extraArgs: []

  # Node scheduling
  nodeSelector: {}
  tolerations: []
  affinity: {}

  # Pod annotations (defaults include Prometheus scrape config)
  podAnnotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8080"
```

### Controller Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `replicas` | `2` | Number of controller replicas |
| `leaderElect` | `true` | Enable leader election |
| `rescanInterval` | `5m` | CRD rescan interval |
| `resources.requests.cpu` | `100m` | CPU request |
| `resources.requests.memory` | `256Mi` | Memory request |
| `resources.limits.cpu` | `500m` | CPU limit |
| `resources.limits.memory` | `512Mi` | Memory limit |

---

## Admission Webhook

```yaml
admissionWebhook:
  # Enable admission webhook for real-time event correlation
  enabled: true

  replicas: 2

  # CRITICAL: Must always be Ignore. Never set to Fail.
  failurePolicy: Ignore

  image:
    repository: ghcr.io/nightjarctl/nightjar-webhook
    tag: ""
    pullPolicy: IfNotPresent

  resources:
    requests:
      cpu: 50m
      memory: 128Mi
    limits:
      cpu: 200m
      memory: 256Mi

  # PodDisruptionBudget for availability
  pdb:
    enabled: true
    minAvailable: 1

  # Certificate management: "cert-manager" or "self-signed"
  certManagement: self-signed
```

### Webhook Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `enabled` | `true` | Deploy admission webhook |
| `replicas` | `2` | Webhook replicas |
| `failurePolicy` | `Ignore` | Webhook failure behavior (NEVER change) |
| `pdb.enabled` | `true` | Enable PodDisruptionBudget |
| `pdb.minAvailable` | `1` | Minimum available pods |
| `certManagement` | `self-signed` | TLS cert strategy |

{: .warning }
> The `failurePolicy` must always be `Ignore`. Setting it to `Fail` would cause Nightjar to block deployments when the webhook is unavailable.

---

## Adapters

```yaml
adapters:
  # Native Kubernetes (always available)
  networkpolicy:
    enabled: true
  resourcequota:
    enabled: true
  webhook:
    enabled: true

  # Policy engines (auto-detected by default)
  cilium:
    enabled: auto  # auto | enabled | disabled
  gatekeeper:
    enabled: auto
  kyverno:
    enabled: auto
  istio:
    enabled: auto
  prometheus:
    enabled: auto
```

### Adapter Modes

| Mode | Behavior |
|------|----------|
| `auto` | Enable if CRDs are installed in cluster |
| `enabled` | Always enable (fails if CRDs missing) |
| `disabled` | Never enable |

### Supported Adapters

| Adapter | CRDs Watched |
|---------|--------------|
| `networkpolicy` | NetworkPolicy |
| `resourcequota` | ResourceQuota, LimitRange |
| `webhook` | ValidatingWebhookConfiguration, MutatingWebhookConfiguration |
| `cilium` | CiliumNetworkPolicy, CiliumClusterwideNetworkPolicy |
| `gatekeeper` | Constraints (all template instances) |
| `kyverno` | ClusterPolicy, Policy |
| `istio` | AuthorizationPolicy, PeerAuthentication |
| `prometheus` | PrometheusRule (for missing alerts) |

---

## Hubble Integration

```yaml
hubble:
  # Enable Hubble flow integration (requires Cilium)
  enabled: false

  # Hubble Relay service address
  relayAddress: hubble-relay.kube-system.svc:4245
```

When enabled, Nightjar correlates Hubble flow drops with network policies for real-time traffic analysis.

---

## Missing Resource Detection

```yaml
requirements:
  # Enable detection of missing companion resources
  enabled: true

  # Debounce period before alerting (avoids sync race conditions)
  debounceSeconds: 120
```

Detects missing resources like:
- ServiceMonitor for workloads with `prometheus.io/scrape` annotation
- VirtualService for workloads with Istio sidecar
- HorizontalPodAutoscaler for high-traffic deployments

---

## Notifications

```yaml
notifications:
  # Create Kubernetes Events on affected workloads
  kubernetesEvents: true

  # Create/update ConstraintReport CRDs per namespace
  constraintReports: true

  # Rate limiting (events per minute per namespace)
  rateLimitPerMinute: 100

  # Slack integration
  slack:
    enabled: false
    webhookUrl: ""
    minSeverity: Critical  # Critical | Warning | Info

  # Generic webhook
  webhook:
    enabled: false
    url: ""

  # Deduplication
  deduplication:
    enabled: true
    # Suppress duplicate notifications for unchanged constraints
    suppressDuplicateMinutes: 60
```

### Notification Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `kubernetesEvents` | `true` | Create K8s Events |
| `constraintReports` | `true` | Create ConstraintReport CRDs |
| `rateLimitPerMinute` | `100` | Rate limit per namespace |
| `slack.enabled` | `false` | Enable Slack notifications |
| `slack.minSeverity` | `Critical` | Minimum severity for Slack |
| `deduplication.enabled` | `true` | Deduplicate notifications |
| `deduplication.suppressDuplicateMinutes` | `60` | Suppression window |

---

## Privacy

```yaml
privacy:
  # Default detail level for developer notifications
  # summary = minimal, detailed = ports/effects, full = everything
  defaultDeveloperDetailLevel: summary

  # Show constraint names from other namespaces
  showCrossNamespacePolicyNames: false

  # Show specific port numbers in developer notifications
  showPortNumbers: false

  # Default contact for remediation guidance
  remediationContact: ""
```

### Privacy Detail Levels

| Level | Developers See |
|-------|---------------|
| `summary` | Constraint exists, type, generic guidance |
| `detailed` | + port numbers, effect details |
| `full` | + cross-namespace policy names, full details |

See [Privacy Model](/nightjar/reference/privacy/) for detailed information.

---

## MCP Server

```yaml
mcp:
  # Enable MCP server for AI agent integration
  enabled: false

  # Port for MCP server
  port: 8090

  # Transport: "sse" for remote agents, "stdio" for local
  transport: sse

  # Authentication method
  authentication:
    # "bearer-token" for external agents
    # "kubernetes-sa" for in-cluster agents
    method: kubernetes-sa
```

See [MCP Server](/nightjar/mcp/) for integration details.

---

## Workload Annotations

```yaml
workloadAnnotations:
  # Annotate affected workloads with constraint summaries
  enabled: true

  # Which workload kinds to annotate
  kinds:
    - Deployment
    - StatefulSet
    - DaemonSet

  # Maximum constraints per workload annotation
  maxConstraintsPerWorkload: 20
```

When enabled, workloads receive annotations like:
```yaml
annotations:
  nightjar.io/constraints: |
    [{"name":"restrict-egress","type":"NetworkEgress","severity":"Critical"}]
  nightjar.io/constraint-count: "3"
  nightjar.io/last-updated: "2024-01-15T10:30:00Z"
```

---

## API Server

```yaml
apiServer:
  # Enable HTTP API for agent discovery
  enabled: true

  # API server port
  port: 8092
```

Endpoints:
- `/api/v1/health` - Health status
- `/api/v1/capabilities` - Adapter status, constraint counts
- `/openapi/v3` - OpenAPI specification

---

## RBAC and ServiceAccount

```yaml
rbac:
  # Create ClusterRole and ClusterRoleBinding
  create: true

serviceAccount:
  # Create ServiceAccount
  create: true

  # ServiceAccount name (generated if empty)
  name: ""

  # Additional annotations (e.g., for workload identity)
  annotations: {}
```

---

## Monitoring

```yaml
monitoring:
  # Prometheus ServiceMonitor
  serviceMonitor:
    enabled: false
    interval: 30s

  # Grafana dashboard ConfigMap
  grafanaDashboard:
    enabled: false
```

When `serviceMonitor.enabled: true`, creates a ServiceMonitor for Prometheus Operator.

---

## Complete Example

```yaml
# Production configuration
controller:
  replicas: 2
  leaderElect: true
  rescanInterval: 5m
  resources:
    requests:
      cpu: 200m
      memory: 512Mi
    limits:
      cpu: 1000m
      memory: 1Gi

admissionWebhook:
  enabled: true
  replicas: 2
  failurePolicy: Ignore
  pdb:
    enabled: true
    minAvailable: 1

adapters:
  networkpolicy:
    enabled: true
  cilium:
    enabled: auto
  gatekeeper:
    enabled: auto
  kyverno:
    enabled: auto

hubble:
  enabled: true
  relayAddress: hubble-relay.kube-system.svc:4245

notifications:
  kubernetesEvents: true
  constraintReports: true
  rateLimitPerMinute: 100
  slack:
    enabled: true
    webhookUrl: "https://hooks.slack.com/services/XXX/YYY/ZZZ"
    minSeverity: Critical
  deduplication:
    enabled: true
    suppressDuplicateMinutes: 60

privacy:
  defaultDeveloperDetailLevel: summary
  showCrossNamespacePolicyNames: false
  remediationContact: "platform-team@company.com"

mcp:
  enabled: true
  port: 8090
  transport: sse

monitoring:
  serviceMonitor:
    enabled: true
    interval: 30s
```
