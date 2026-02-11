---
layout: default
title: Installation
parent: Getting Started
nav_order: 1
---

# Installation
{: .no_toc }

## Table of contents
{: .no_toc .text-delta }

1. TOC
{:toc}

---

## Helm Installation

### Add the Helm Repository

```bash
helm repo add nightjar https://nightjarctl.github.io/charts
helm repo update
```

### Install with Default Settings

```bash
helm install nightjar nightjar/nightjar \
  -n nightjar-system \
  --create-namespace
```

This installs:
- Controller with 2 replicas (leader election enabled)
- Admission webhook with 2 replicas
- RBAC with cluster-wide read access
- ServiceAccount with necessary permissions

### Verify Installation

```bash
# Check pods are running
kubectl get pods -n nightjar-system

# Check CRDs are installed
kubectl get crd | grep nightjar

# View controller logs
kubectl logs -n nightjar-system -l app=nightjar-controller
```

Expected CRDs:
```
constraintreports.nightjar.io
constraintprofiles.nightjar.io
notificationpolicies.nightjar.io
```

---

## Configuration

### Minimal Production Configuration

```yaml
# values.yaml
controller:
  replicas: 2
  leaderElect: true
  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: 500m
      memory: 512Mi

admissionWebhook:
  enabled: true
  replicas: 2
  failurePolicy: Ignore  # MUST be Ignore for safety

notifications:
  kubernetesEvents: true
  constraintReports: true

privacy:
  defaultDeveloperDetailLevel: summary
  remediationContact: "platform-team@yourcompany.com"
```

Install with custom values:

```bash
helm install nightjar nightjar/nightjar \
  -n nightjar-system \
  --create-namespace \
  -f values.yaml
```

### Enable Slack Notifications

```yaml
notifications:
  slack:
    enabled: true
    webhookUrl: "https://hooks.slack.com/services/XXX/YYY/ZZZ"
    minSeverity: Critical  # Only Critical alerts
```

### Enable MCP Server for AI Agents

```yaml
mcp:
  enabled: true
  port: 8090
  transport: sse
  authentication:
    method: kubernetes-sa
```

### Enable Hubble Integration (Cilium)

```yaml
hubble:
  enabled: true
  relayAddress: hubble-relay.kube-system.svc:4245
```

---

## CLI Installation

### Download Binary

Pre-built binaries are available from [GitHub Releases](https://github.com/cendertdev/nightjar/releases).

**Linux (amd64)**:
```bash
curl -sL https://github.com/cendertdev/nightjar/releases/latest/download/nightjarctl-linux-amd64 -o nightjar
chmod +x nightjar
sudo mv nightjar /usr/local/bin/
```

**Linux (arm64)**:
```bash
curl -sL https://github.com/cendertdev/nightjar/releases/latest/download/nightjarctl-linux-arm64 -o nightjar
chmod +x nightjar
sudo mv nightjar /usr/local/bin/
```

**macOS (Apple Silicon)**:
```bash
curl -sL https://github.com/cendertdev/nightjar/releases/latest/download/nightjarctl-darwin-arm64 -o nightjar
chmod +x nightjar
sudo mv nightjar /usr/local/bin/
```

**macOS (Intel)**:
```bash
curl -sL https://github.com/cendertdev/nightjar/releases/latest/download/nightjarctl-darwin-amd64 -o nightjar
chmod +x nightjar
sudo mv nightjar /usr/local/bin/
```

**Windows (amd64)**:
```powershell
Invoke-WebRequest -Uri https://github.com/cendertdev/nightjar/releases/latest/download/nightjarctl-windows-amd64.exe -OutFile nightjar.exe
Move-Item nightjar.exe C:\Windows\System32\
```

**Verify checksum** (optional, replace platform suffix as needed):
```bash
curl -sL https://github.com/cendertdev/nightjar/releases/latest/download/nightjarctl-linux-amd64.sha256 -o nightjar.sha256
sha256sum -c nightjar.sha256
```

### Using Go

Requires Go 1.21+.

```bash
go install github.com/nightjarctl/nightjar/cmd/nightjarctl@latest
```

The binary is named `nightjar`. Verify installation:

```bash
nightjar version
nightjar --help
```

### From Source

```bash
git clone https://github.com/nightjarctl/nightjar.git
cd nightjar
make build
mv bin/nightjar /usr/local/bin/
```

### kubectl Plugin (Alternative)

The CLI can also be invoked as a kubectl plugin:

```bash
# Download binary (Linux amd64)
curl -sL https://github.com/cendertdev/nightjar/releases/latest/download/nightjar-sentinel-linux-amd64 -o kubectl-sentinel
chmod +x kubectl-sentinel
sudo mv kubectl-sentinel /usr/local/bin/

# Or via Go (requires Go 1.21+)
# go install github.com/nightjarctl/nightjar/cmd/kubectl-sentinel@latest

# Use
kubectl sentinel query -n my-namespace
kubectl sentinel explain -n my-namespace "connection refused"
```

---

## RBAC Details

### Controller ClusterRole

The controller requires broad read access:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: nightjar-controller
rules:
  # Read all resources to discover policies
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]

  # Write Nightjar CRDs
  - apiGroups: ["nightjar.io"]
    resources: ["constraintreports", "constraintreports/status"]
    verbs: ["create", "update", "patch", "delete"]

  # Create Events on workloads
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create", "patch"]
```

### Webhook ClusterRole

The admission webhook has minimal permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: nightjar-webhook
rules:
  # Read Nightjar CRDs for constraint lookup
  - apiGroups: ["nightjar.io"]
    resources: ["constraintreports"]
    verbs: ["get", "list"]
```

---

## High Availability

### Controller HA

With `controller.replicas: 2` and `controller.leaderElect: true`, one controller is active while the other is standby. Failover is automatic.

### Webhook HA

With `admissionWebhook.replicas: 2` and `admissionWebhook.pdb.enabled: true`, the webhook maintains availability during rolling updates.

### Pod Disruption Budgets

```yaml
admissionWebhook:
  pdb:
    enabled: true
    minAvailable: 1
```

---

## Certificate Management

The admission webhook requires TLS certificates.

### Self-Signed (Default)

```yaml
admissionWebhook:
  certManagement: self-signed
```

Nightjar generates certificates automatically and rotates them before expiry.

### cert-manager

```yaml
admissionWebhook:
  certManagement: cert-manager
```

Requires cert-manager installed in the cluster. Nightjar creates a Certificate resource.

---

## Uninstallation

```bash
# Remove Helm release
helm uninstall nightjar -n nightjar-system

# Remove CRDs (optional - deletes all ConstraintReports)
kubectl delete crd constraintreports.nightjar.io
kubectl delete crd constraintprofiles.nightjar.io
kubectl delete crd notificationpolicies.nightjar.io

# Remove namespace
kubectl delete namespace nightjar-system
```

---

## Troubleshooting

### Controller Not Starting

Check logs:
```bash
kubectl logs -n nightjar-system -l app=nightjar-controller
```

Common issues:
- **RBAC errors**: Ensure ClusterRole and ClusterRoleBinding are created
- **CRD not found**: Run `helm install` again or check Helm hooks

### No Constraints Discovered

1. Verify adapters are enabled:
   ```bash
   kubectl get constraintprofiles
   ```

2. Check controller logs for adapter errors:
   ```bash
   kubectl logs -n nightjar-system -l app=nightjar-controller | grep adapter
   ```

3. Verify policy CRDs exist:
   ```bash
   kubectl get crd | grep -E 'networkpolic|gatekeeper|kyverno'
   ```

### Webhook Not Receiving Events

1. Check webhook registration:
   ```bash
   kubectl get validatingwebhookconfiguration nightjar-webhook
   ```

2. Verify failurePolicy is `Ignore`:
   ```bash
   kubectl get validatingwebhookconfiguration nightjar-webhook -o yaml | grep failurePolicy
   ```

3. Check webhook pod logs:
   ```bash
   kubectl logs -n nightjar-system -l app=nightjar-webhook
   ```
