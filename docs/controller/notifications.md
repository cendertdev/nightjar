---
layout: default
title: Notifications
parent: Controller
nav_order: 3
---

# Notifications
{: .no_toc }

Configure how Nightjar notifies developers about constraints.
{: .fs-6 .fw-300 }

## Table of contents
{: .no_toc .text-delta }

1. TOC
{:toc}

---

## Overview

Nightjar delivers constraint information through multiple channels:

| Channel | Purpose | Enabled By Default |
|---------|---------|-------------------|
| Kubernetes Events | Real-time alerts on workloads | Yes |
| ConstraintReport CRDs | Structured data for tooling | Yes |
| Workload Annotations | Labels for kubectl/UIs | Yes |
| Slack | Team alerting | No |
| Webhook | Custom integrations | No |

---

## Kubernetes Events

Events are created on affected workloads when constraints are discovered or change.

### Configuration

```yaml
notifications:
  kubernetesEvents: true
  rateLimitPerMinute: 100  # Per namespace
```

### Event Format

```yaml
apiVersion: v1
kind: Event
metadata:
  name: my-deployment.constraint-discovered
  namespace: my-namespace
type: Warning
reason: ConstraintDiscovered
message: |
  NetworkPolicy 'restrict-egress' restricts egress from this workload.
  Allowed ports: 443, 8443. Contact platform-team@company.com for exceptions.
involvedObject:
  apiVersion: apps/v1
  kind: Deployment
  name: my-deployment
```

### Viewing Events

```bash
# Events on a specific workload
kubectl describe deployment my-deployment

# All constraint events in namespace
kubectl get events -n my-namespace --field-selector reason=ConstraintDiscovered
```

---

## ConstraintReport CRDs

A ConstraintReport is created per namespace containing all constraints.

### Configuration

```yaml
notifications:
  constraintReports: true
```

### Report Format

```yaml
apiVersion: nightjar.io/v1alpha1
kind: ConstraintReport
metadata:
  name: constraints
  namespace: my-namespace
status:
  constraintCount: 3
  criticalCount: 1
  warningCount: 1
  infoCount: 1
  lastUpdated: "2024-01-15T10:30:00Z"

  constraints:
    - name: restrict-egress
      type: NetworkEgress
      severity: Critical
      message: "Egress restricted to ports 443, 8443"
      source: NetworkPolicy
      lastSeen: "2024-01-15T10:30:00Z"

  machineReadable:
    schemaVersion: "1"
    detailLevel: summary
    constraints:
      - uid: abc123
        name: restrict-egress
        constraintType: NetworkEgress
        severity: Critical
        effect: deny
        sourceRef:
          apiVersion: networking.k8s.io/v1
          kind: NetworkPolicy
          name: restrict-egress
          namespace: my-namespace
        remediation:
          summary: "Request network policy exception"
          steps:
            - type: manual
              description: "Contact platform team"
              contact: "platform-team@company.com"
        tags: [network, egress]
```

### Viewing Reports

```bash
# List all reports
kubectl get constraintreports -A

# View specific report
kubectl get constraintreport constraints -n my-namespace -o yaml

# JSON output for tooling
kubectl get constraintreport constraints -n my-namespace -o json | jq '.status.machineReadable'
```

---

## Workload Annotations

Nightjar annotates affected workloads with constraint summaries.

### Configuration

```yaml
workloadAnnotations:
  enabled: true
  kinds:
    - Deployment
    - StatefulSet
    - DaemonSet
  maxConstraintsPerWorkload: 20
```

### Annotation Format

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  annotations:
    nightjar.io/constraints: |
      [
        {"name":"restrict-egress","type":"NetworkEgress","severity":"Critical"},
        {"name":"compute-quota","type":"ResourceLimit","severity":"Warning"}
      ]
    nightjar.io/constraint-count: "2"
    nightjar.io/critical-count: "1"
    nightjar.io/last-updated: "2024-01-15T10:30:00Z"
```

### Viewing Annotations

```bash
# View constraint annotations
kubectl get deployment my-app -o jsonpath='{.metadata.annotations.nightjar\.io/constraints}' | jq

# List workloads with critical constraints
kubectl get deployments -A -o json | jq -r '
  .items[] |
  select(.metadata.annotations["nightjar.io/critical-count"] | tonumber > 0) |
  "\(.metadata.namespace)/\(.metadata.name)"
'
```

---

## Slack Integration

Send alerts to Slack channels.

### Configuration

```yaml
notifications:
  slack:
    enabled: true
    webhookUrl: "https://hooks.slack.com/services/XXX/YYY/ZZZ"
    minSeverity: Critical  # Only Critical alerts
```

### Creating a Webhook

1. Go to [Slack API](https://api.slack.com/apps)
2. Create a new app or select existing
3. Enable "Incoming Webhooks"
4. Add webhook to desired channel
5. Copy webhook URL

### Message Format

```
:warning: *Constraint Discovered*

*Namespace:* production
*Workload:* api-server
*Constraint:* restrict-egress (NetworkPolicy)
*Severity:* Critical
*Effect:* Egress restricted to ports 443, 8443

*Remediation:* Contact platform-team@company.com for exceptions
```

### Severity Filtering

| minSeverity | Notifications Sent |
|-------------|-------------------|
| `Critical` | Only Critical |
| `Warning` | Critical + Warning |
| `Info` | All constraints |

---

## Generic Webhook

Send JSON payloads to any HTTP endpoint.

### Configuration

```yaml
notifications:
  webhook:
    enabled: true
    url: "https://your-service.example.com/nightjar-webhook"
```

### Payload Format

```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "namespace": "production",
  "workload": {
    "kind": "Deployment",
    "name": "api-server"
  },
  "constraint": {
    "name": "restrict-egress",
    "type": "NetworkEgress",
    "severity": "Critical",
    "effect": "deny",
    "source": "NetworkPolicy"
  },
  "message": "Egress restricted to ports 443, 8443",
  "remediation": {
    "summary": "Request network policy exception",
    "contact": "platform-team@company.com"
  }
}
```

### Authentication

For authenticated endpoints, use a service mesh sidecar or configure the webhook URL with basic auth:

```yaml
notifications:
  webhook:
    url: "https://user:password@your-service.example.com/webhook"
```

---

## Deduplication

Prevent notification spam for unchanged constraints.

### Configuration

```yaml
notifications:
  deduplication:
    enabled: true
    suppressDuplicateMinutes: 60
```

### Behavior

- First notification: Always sent
- Subsequent: Suppressed if constraint unchanged
- After timeout: Re-sent if still present
- On change: Immediately sent

---

## Rate Limiting

Prevent overwhelming notification channels.

### Configuration

```yaml
notifications:
  rateLimitPerMinute: 100
```

Rate limit is per namespace. When exceeded:
- Events continue (K8s handles backpressure)
- Slack/Webhook queued and sent later
- ConstraintReports always updated

---

## Privacy and Detail Levels

Notifications are scoped based on the audience.

### Configuration

```yaml
privacy:
  defaultDeveloperDetailLevel: summary
  showCrossNamespacePolicyNames: false
  showPortNumbers: false
  remediationContact: "platform-team@company.com"
```

### What Each Level Shows

| Level | Constraint Name | Ports | Cross-NS Details |
|-------|----------------|-------|------------------|
| `summary` | Same NS only | No | No |
| `detailed` | Same NS only | Yes | No |
| `full` | All | Yes | Yes |

### NotificationPolicy CRD

For fine-grained control, create a NotificationPolicy:

```yaml
apiVersion: nightjar.io/v1alpha1
kind: NotificationPolicy
metadata:
  name: default
spec:
  developerScope:
    showConstraintType: true
    showConstraintName: "same-namespace-only"
    showAffectedPorts: false
    showRemediationContact: true
    contact: "platform-team@company.com"
    maxDetailLevel: summary

  platformAdminScope:
    showConstraintName: "all"
    showAffectedPorts: true
    maxDetailLevel: full

  platformAdminRoles:
    - cluster-admin
    - platform-admin

  channels:
    slack:
      enabled: true
      webhookUrl: "https://hooks.slack.com/services/XXX"
      minSeverity: Critical
```

---

## Troubleshooting

### Events Not Appearing

```bash
# Check controller can create events
kubectl auth can-i create events --as=system:serviceaccount:nightjar-system:nightjar-controller

# Check controller logs
kubectl logs -n nightjar-system -l app=nightjar-controller | grep "event"
```

### Slack Not Receiving Messages

```bash
# Test webhook manually
curl -X POST -H 'Content-type: application/json' \
  --data '{"text":"Test message"}' \
  https://hooks.slack.com/services/XXX/YYY/ZZZ

# Check controller logs
kubectl logs -n nightjar-system -l app=nightjar-controller | grep "slack"
```

### Cluster-Scoped Constraints

Cluster-scoped constraints (e.g., `ValidatingWebhookConfiguration`, Gatekeeper `ConstraintTemplate` instances) affect all namespaces. When a cluster-scoped constraint has explicit `AffectedNamespaces`, only those namespaces are updated. When it has none, Nightjar triggers a cluster-wide reconciliation: it lists all namespaces and updates the ConstraintReport and workload annotations in each.

This applies to both ConstraintReport reconciliation and workload annotation. Debounce timers prevent excessive reconciliation from rapid cluster-scoped changes.

### ConstraintReports Not Updating

```bash
# Check CRD exists
kubectl get crd constraintreports.nightjar.io

# Check controller logs
kubectl logs -n nightjar-system -l app=nightjar-controller | grep "report"

# Force rescan
kubectl rollout restart deployment -n nightjar-system nightjar-controller
```
