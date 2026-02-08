---
layout: default
title: CRDs
nav_order: 6
has_children: true
permalink: /crds/
---

# Custom Resource Definitions
{: .no_toc }

Nightjar uses three CRDs to store and configure constraint data.
{: .fs-6 .fw-300 }

---

## Overview

| CRD | Scope | Purpose |
|-----|-------|---------|
| [ConstraintReport](constraintreport/) | Namespaced | Stores discovered constraints per namespace |
| [ConstraintProfile](constraintprofile/) | Cluster | Configures how CRDs are parsed |
| [NotificationPolicy](notificationpolicy/) | Cluster | Controls privacy and notification channels |

---

## Installation

CRDs are installed automatically by the Helm chart:

```bash
helm install nightjar nightjar/nightjar -n nightjar-system --create-namespace
```

Verify installation:
```bash
kubectl get crd | grep nightjar.io
```

Expected output:
```
constraintprofiles.nightjar.io      2024-01-15T10:00:00Z
constraintreports.nightjar.io       2024-01-15T10:00:00Z
notificationpolicies.nightjar.io    2024-01-15T10:00:00Z
```

---

## CRD Hierarchy

```
                    ┌─────────────────────────┐
                    │  NotificationPolicy     │
                    │  (cluster-scoped)       │
                    │  - Privacy settings     │
                    │  - Channel config       │
                    └───────────┬─────────────┘
                                │
                                │ controls detail level
                                ▼
┌─────────────────────────┐    ┌─────────────────────────┐
│  ConstraintProfile      │    │  ConstraintReport       │
│  (cluster-scoped)       │    │  (namespace-scoped)     │
│  - CRD registration     │───▶│  - Constraint entries   │
│  - Adapter config       │    │  - Machine-readable     │
└─────────────────────────┘    └─────────────────────────┘
```

---

## Who Creates What

| CRD | Created By | When |
|-----|------------|------|
| ConstraintReport | Controller (auto) | When constraints affect a namespace |
| ConstraintProfile | Platform admin (manual) | To register custom policy CRDs |
| NotificationPolicy | Platform admin (manual) | To configure privacy/channels |

---

## RBAC Requirements

### Developers

Read ConstraintReports in their namespace:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: nightjar-reader
  namespace: my-namespace
rules:
  - apiGroups: ["nightjar.io"]
    resources: ["constraintreports"]
    verbs: ["get", "list"]
```

### Platform Admins

Full access to all Nightjar CRDs:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: nightjar-admin
rules:
  - apiGroups: ["nightjar.io"]
    resources: ["*"]
    verbs: ["*"]
```

---

## API Group

All Nightjar CRDs use the API group:
```
nightjar.io/v1alpha1
```

Full resource names:
- `constraintreports.nightjar.io`
- `constraintprofiles.nightjar.io`
- `notificationpolicies.nightjar.io`

---

## Short Names

| CRD | Short Name | Example |
|-----|------------|---------|
| ConstraintReport | `cr` | `kubectl get cr -n my-namespace` |
| ConstraintProfile | `cp` | `kubectl get cp` |
| NotificationPolicy | `np` | `kubectl get np` |

---

## What's Next

- [ConstraintReport](constraintreport/) - Per-namespace constraint data
- [ConstraintProfile](constraintprofile/) - Register custom policy CRDs
- [NotificationPolicy](notificationpolicy/) - Privacy and channel configuration
