# Examples

Standalone Kubernetes manifests for trying out Nightjar.

## Prerequisites

- A running Kubernetes cluster
- Nightjar installed (see [Installation](../docs/getting-started/installation.md))
- `kubectl` configured to access the cluster

## Files

| File | Scope | Description |
|------|-------|-------------|
| `network-policy.yaml` | Namespaced | Egress-restricting NetworkPolicy (allows DNS + HTTPS only) |
| `resource-quota.yaml` | Namespaced | CPU and memory ResourceQuota |
| `deployment.yaml` | Namespaced | Sample Deployment without resource limits (intentionally triggers quota warnings) |
| `notification-policy.yaml` | Cluster | NotificationPolicy with developer/admin scoping and Slack config |
| `constraint-profile.yaml` | Cluster | ConstraintProfile for registering a custom CRD with Nightjar |

## Usage

### Apply namespace-scoped examples

These examples are safe to apply to any test namespace:

```bash
kubectl create namespace nightjar-demo

kubectl apply -f examples/network-policy.yaml -n nightjar-demo
kubectl apply -f examples/resource-quota.yaml -n nightjar-demo
kubectl apply -f examples/deployment.yaml -n nightjar-demo
```

After a few seconds, check the ConstraintReport:

```bash
kubectl get constraintreport -n nightjar-demo
```

### Apply cluster-scoped examples

These require Nightjar CRDs to be installed and cluster-admin access:

```bash
kubectl apply -f examples/notification-policy.yaml
kubectl apply -f examples/constraint-profile.yaml
```

### Clean up

```bash
kubectl delete namespace nightjar-demo
kubectl delete notificationpolicy example-notification-policy
kubectl delete constraintprofile company-deployment-restrictions cilium-network-policies
```
