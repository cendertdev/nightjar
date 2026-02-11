# Nightjar Helm Chart

Automatic constraint discovery and developer notification for Kubernetes.

## Description

Nightjar is a Kubernetes operator that discovers all policies, constraints, quotas, and requirements across your cluster — regardless of which policy engine created them — and notifies developers when those constraints block their workloads.

This chart deploys:
- **Controller** — Discovers and indexes constraints, dispatches notifications
- **Admission Webhook** — Real-time deploy-time warnings (always fail-open)
- **CRDs** — ConstraintReport, ConstraintProfile, NotificationPolicy

## Prerequisites

- Kubernetes 1.24+
- Helm 3.10+
- (Optional) cert-manager for webhook TLS certificate management

## Installation

```bash
helm repo add nightjar https://nightjarctl.github.io/charts
helm repo update

helm install nightjar nightjar/nightjar \
  -n nightjar-system \
  --create-namespace
```

### Install with custom values

```bash
helm install nightjar nightjar/nightjar \
  -n nightjar-system \
  --create-namespace \
  -f values.yaml
```

### Verify

```bash
kubectl get pods -n nightjar-system
kubectl get crd | grep nightjar
```

## Values

The table below lists the most commonly configured parameters. See the [full configuration reference](https://github.com/cendertdev/nightjar/blob/master/docs/controller/configuration.md) for all options, or inspect [`values.yaml`](values.yaml) directly.

### Controller

| Parameter | Default | Description |
|-----------|---------|-------------|
| `controller.replicas` | `2` | Number of controller replicas |
| `controller.leaderElect` | `true` | Enable leader election for HA |
| `controller.rescanInterval` | `5m` | How often to scan for new CRDs |
| `controller.image.repository` | `ghcr.io/cendertdev/nightjar` | Controller image |
| `controller.image.tag` | `""` (appVersion) | Image tag |
| `controller.resources.requests.cpu` | `100m` | CPU request |
| `controller.resources.requests.memory` | `256Mi` | Memory request |

### Admission Webhook

| Parameter | Default | Description |
|-----------|---------|-------------|
| `admissionWebhook.enabled` | `true` | Deploy admission webhook |
| `admissionWebhook.replicas` | `2` | Webhook replicas |
| `admissionWebhook.failurePolicy` | `Ignore` | **Must always be `Ignore`** — never set to `Fail` |
| `admissionWebhook.timeoutSeconds` | `5` | Webhook timeout |
| `admissionWebhook.certManagement` | `self-signed` | TLS cert strategy (`self-signed` or `cert-manager`) |
| `admissionWebhook.pdb.enabled` | `true` | Enable PodDisruptionBudget |

### Adapters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `adapters.networkpolicy.enabled` | `true` | NetworkPolicy adapter (native K8s) |
| `adapters.resourcequota.enabled` | `true` | ResourceQuota adapter (native K8s) |
| `adapters.webhook.enabled` | `true` | WebhookConfiguration adapter (native K8s) |
| `adapters.cilium.enabled` | `auto` | Cilium adapter (`auto`, `enabled`, `disabled`) |
| `adapters.gatekeeper.enabled` | `auto` | Gatekeeper/OPA adapter |
| `adapters.kyverno.enabled` | `auto` | Kyverno adapter |
| `adapters.istio.enabled` | `auto` | Istio adapter |
| `adapters.prometheus.enabled` | `auto` | Prometheus adapter |

### Notifications

| Parameter | Default | Description |
|-----------|---------|-------------|
| `notifications.kubernetesEvents` | `true` | Create K8s Events on affected workloads |
| `notifications.constraintReports` | `true` | Create ConstraintReport CRDs per namespace |
| `notifications.rateLimitPerMinute` | `100` | Max events per minute per namespace |
| `notifications.slack.enabled` | `false` | Enable Slack notifications |
| `notifications.slack.webhookUrl` | `""` | Slack incoming webhook URL |
| `notifications.slack.minSeverity` | `Critical` | Minimum severity for Slack alerts |
| `notifications.deduplication.enabled` | `true` | Suppress duplicate notifications |

### Privacy

| Parameter | Default | Description |
|-----------|---------|-------------|
| `privacy.defaultDeveloperDetailLevel` | `summary` | Detail level: `summary`, `detailed`, `full` |
| `privacy.showCrossNamespacePolicyNames` | `false` | Show constraint names from other namespaces |
| `privacy.showPortNumbers` | `false` | Show specific port numbers in developer notifications |
| `privacy.remediationContact` | `""` | Default contact for remediation hints |

### Optional Features

| Parameter | Default | Description |
|-----------|---------|-------------|
| `hubble.enabled` | `false` | Enable Cilium Hubble flow integration |
| `hubble.relayAddress` | `hubble-relay.kube-system.svc:4245` | Hubble Relay address |
| `mcp.enabled` | `false` | Enable MCP server for AI agent integration |
| `mcp.port` | `8090` | MCP server port |
| `requirements.enabled` | `true` | Enable missing resource detection |
| `requirements.debounceSeconds` | `120` | Debounce period before alerting |
| `monitoring.serviceMonitor.enabled` | `false` | Create Prometheus ServiceMonitor |
| `apiServer.enabled` | `true` | Enable HTTP API server |

### Infrastructure

| Parameter | Default | Description |
|-----------|---------|-------------|
| `rbac.create` | `true` | Create ClusterRole and ClusterRoleBinding |
| `serviceAccount.create` | `true` | Create ServiceAccount |
| `serviceAccount.name` | `""` | ServiceAccount name (auto-generated if empty) |
| `workloadAnnotations.enabled` | `true` | Annotate workloads with constraint summaries |

## Examples

### Enable Slack notifications

```yaml
notifications:
  slack:
    enabled: true
    webhookUrl: "https://hooks.slack.com/services/XXX/YYY/ZZZ"
    minSeverity: Critical
```

### Enable MCP server for AI agents

```yaml
mcp:
  enabled: true
  port: 8090
  transport: sse
  authentication:
    method: kubernetes-sa
```

### Enable Hubble integration

```yaml
hubble:
  enabled: true
  relayAddress: hubble-relay.kube-system.svc:4245
```

### Production configuration

```yaml
controller:
  replicas: 2
  leaderElect: true
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
  certManagement: cert-manager

notifications:
  kubernetesEvents: true
  constraintReports: true
  slack:
    enabled: true
    webhookUrl: "https://hooks.slack.com/services/XXX/YYY/ZZZ"
    minSeverity: Critical
  deduplication:
    enabled: true

privacy:
  defaultDeveloperDetailLevel: summary
  remediationContact: "platform-team@company.com"

monitoring:
  serviceMonitor:
    enabled: true
```

## Uninstall

```bash
helm uninstall nightjar -n nightjar-system

# Remove CRDs (optional — deletes all ConstraintReports)
kubectl delete crd constraintreports.nightjar.io
kubectl delete crd constraintprofiles.nightjar.io
kubectl delete crd notificationpolicies.nightjar.io

kubectl delete namespace nightjar-system
```

## Links

- [Full Configuration Reference](https://github.com/cendertdev/nightjar/blob/master/docs/controller/configuration.md)
- [Getting Started](https://github.com/cendertdev/nightjar/blob/master/docs/getting-started/quickstart.md)
- [Architecture](https://github.com/cendertdev/nightjar/blob/master/docs/ARCHITECTURE.md)
- [Privacy Model](https://github.com/cendertdev/nightjar/blob/master/docs/PRIVACY_MODEL.md)
- [Source Code](https://github.com/cendertdev/nightjar)
