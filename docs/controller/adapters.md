---
layout: default
title: Adapters
parent: Controller
nav_order: 2
---

# Adapters
{: .no_toc }

Adapters parse policy engine CRDs into normalized constraints.
{: .fs-6 .fw-300 }

## Table of contents
{: .no_toc .text-delta }

1. TOC
{:toc}

---

## Overview

Each policy engine has a different CRD schema. Adapters normalize them into a common `Constraint` model:

```
NetworkPolicy        ─┐
CiliumNetworkPolicy  ─┼─▶ Adapter ─▶ Constraint{Type, Severity, Effect, ...}
K8sRequiredLabels    ─┘
```

---

## Built-in Adapters

### networkpolicy

Parses Kubernetes NetworkPolicy resources.

**Watched Resources:**
- `networking.k8s.io/v1/NetworkPolicy`

**Constraint Types Generated:**
- `NetworkIngress` - When `spec.policyTypes` includes "Ingress"
- `NetworkEgress` - When `spec.policyTypes` includes "Egress"

**Example Constraint:**
```yaml
Name: restrict-egress
Type: NetworkEgress
Severity: Critical
Effect: deny
Summary: "Egress restricted to ports 443, 8443"
Tags: [network, egress, port-restriction]
```

---

### resourcequota

Parses ResourceQuota and LimitRange resources.

**Watched Resources:**
- `v1/ResourceQuota`
- `v1/LimitRange`

**Constraint Types Generated:**
- `ResourceLimit`

**Metrics Extracted:**
- CPU usage percentage
- Memory usage percentage
- Storage usage percentage
- Pod count

**Example Constraint:**
```yaml
Name: compute-quota
Type: ResourceLimit
Severity: Warning
Effect: limit
Summary: "CPU usage at 78% of quota"
Details:
  resources:
    cpu:
      hard: "4"
      used: "3.12"
      percent: 78
    memory:
      hard: "8Gi"
      used: "6Gi"
      percent: 75
```

---

### webhook

Parses admission webhook configurations.

**Watched Resources:**
- `admissionregistration.k8s.io/v1/ValidatingWebhookConfiguration`
- `admissionregistration.k8s.io/v1/MutatingWebhookConfiguration`

**Constraint Types Generated:**
- `Admission`

**Example Constraint:**
```yaml
Name: require-labels
Type: Admission
Severity: Critical
Effect: deny
Summary: "ValidatingWebhook may reject pods"
```

---

### cilium

Parses Cilium network policies.

**Watched Resources:**
- `cilium.io/v2/CiliumNetworkPolicy`
- `cilium.io/v2/CiliumClusterwideNetworkPolicy`

**Constraint Types Generated:**
- `NetworkIngress`
- `NetworkEgress`

**Example Constraint:**
```yaml
Name: allow-dns-only
Type: NetworkEgress
Severity: Critical
Effect: deny
Summary: "Egress allowed only to kube-dns"
Tags: [network, egress, cilium, dns-only]
```

---

### gatekeeper

Parses OPA Gatekeeper constraints.

**Watched Resources:**
- All CRDs created from ConstraintTemplates
- Detected by `constraints.gatekeeper.sh` API group

**Constraint Types Generated:**
- `Admission`

**Example Constraint:**
```yaml
Name: k8srequiredlabels-must-have-team
Type: Admission
Severity: Critical
Effect: deny
Summary: "Gatekeeper: pods must have 'team' label"
Tags: [admission, gatekeeper, labels]
```

---

### kyverno

Parses Kyverno policies.

**Watched Resources:**
- `kyverno.io/v1/ClusterPolicy`
- `kyverno.io/v1/Policy`

**Constraint Types Generated:**
- `Admission`

**Example Constraint:**
```yaml
Name: require-resource-limits
Type: Admission
Severity: Critical
Effect: deny
Summary: "Kyverno: containers must have resource limits"
Tags: [admission, kyverno, resources]
```

---

### istio

Parses Istio authorization policies.

**Watched Resources:**
- `security.istio.io/v1beta1/AuthorizationPolicy`
- `security.istio.io/v1beta1/PeerAuthentication`

**Constraint Types Generated:**
- `MeshPolicy`

**Example Constraint:**
```yaml
Name: deny-external
Type: MeshPolicy
Severity: Critical
Effect: deny
Summary: "Istio: denies traffic from external sources"
Tags: [mesh, istio, authorization]
```

---

### generic

Fallback adapter for unknown CRDs registered via ConstraintProfile.

**Watched Resources:**
- Any CRD registered in a ConstraintProfile with `adapter: generic`

**Constraint Types Generated:**
- `Unknown`

---

## Enabling Adapters

### Auto-Detection (Default)

```yaml
adapters:
  cilium:
    enabled: auto  # Enable if CRDs exist
```

The adapter enables automatically when:
1. The relevant CRDs are installed
2. The controller has RBAC access

### Force Enable

```yaml
adapters:
  cilium:
    enabled: enabled  # Always enable
```

The controller will fail to start if CRDs are missing.

### Disable

```yaml
adapters:
  gatekeeper:
    enabled: disabled  # Never enable
```

Useful when you have CRDs installed but don't want Nightjar to watch them.

---

## Custom Adapters via ConstraintProfile

Register custom policy CRDs using the ConstraintProfile CRD:

```yaml
apiVersion: nightjar.io/v1alpha1
kind: ConstraintProfile
metadata:
  name: custom-network-policy
spec:
  gvr:
    group: custom.example.com
    version: v1
    resource: networkrules
  adapter: generic  # Use generic adapter
  enabled: true
  severity: Warning  # Override default severity
  debounceSeconds: 300
```

The generic adapter extracts:
- Name from `metadata.name`
- Namespace from `metadata.namespace`
- Labels from `metadata.labels`
- Summary from `spec.description` (if present)

---

## Adapter Health

Check adapter status via the API:

```bash
curl http://nightjar-controller:8092/api/v1/capabilities
```

Response:
```json
{
  "adapters": [
    "networkpolicy",
    "resourcequota",
    "webhook",
    "cilium",
    "gatekeeper"
  ],
  "constraintTypes": {
    "NetworkIngress": 5,
    "NetworkEgress": 12,
    "Admission": 8,
    "ResourceLimit": 3
  }
}
```

Or via MCP:
```json
{
  "tool": "nightjar_list_namespaces"
}
```

---

## Adapter Error Handling

When an adapter fails to parse a resource:

1. **Log Error**: Detailed error in controller logs
2. **Metric Increment**: `nightjar_adapter_parse_errors` counter
3. **Continue Processing**: Other resources still processed
4. **No Constraint Created**: Unparseable resources are skipped

Errors are typically caused by:
- Unexpected CRD schema changes
- Nil field access (fixed by safe field helpers)
- Version mismatches

---

## Writing Custom Adapters

For in-tree adapters, follow this pattern:

{% raw %}
```go
// internal/adapters/myengine/adapter.go
package myengine

import (
    "context"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/apimachinery/pkg/runtime/schema"
    "github.com/nightjarctl/nightjar/internal/types"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Name() string { return "myengine" }

func (a *Adapter) Handles() []schema.GroupVersionResource {
    return []schema.GroupVersionResource{
        {Group: "myengine.io", Version: "v1", Resource: "policies"},
    }
}

func (a *Adapter) Parse(
    ctx context.Context,
    obj *unstructured.Unstructured,
) ([]types.Constraint, error) {
    // Use internal/util for safe field access
    name := util.GetString(obj.Object, "metadata", "name")

    return []types.Constraint{{
        UID:            obj.GetUID(),
        Name:           name,
        Namespace:      obj.GetNamespace(),
        ConstraintType: types.ConstraintTypeAdmission,
        Severity:       types.SeverityWarning,
        Effect:         "warn",
        Summary:        "Custom policy applies",
    }}, nil
}
```
{% endraw %}

Register in `internal/adapters/registry.go`.

---

## Troubleshooting

### Adapter Not Detecting CRDs

```bash
# Check CRD exists
kubectl get crd ciliumnetworkpolicies.cilium.io

# Check controller logs
kubectl logs -n nightjar-system -l app=nightjar-controller | grep cilium

# Check adapter status
curl http://nightjar-controller:8092/api/v1/capabilities
```

### Parse Errors

```bash
# Check for parse errors
kubectl logs -n nightjar-system -l app=nightjar-controller | grep "parse error"

# Check metrics
curl http://nightjar-controller:8080/metrics | grep adapter_parse_errors
```

### RBAC Issues

```bash
# Verify ClusterRole has access
kubectl auth can-i get ciliumnetworkpolicies --as=system:serviceaccount:nightjar-system:nightjar-controller
```
