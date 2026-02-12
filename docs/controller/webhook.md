---
layout: default
title: Admission Webhook
parent: Controller
nav_order: 4
---

# Admission Webhook
{: .no_toc }

A separate deployment that warns developers about policy constraints at deploy time, without ever blocking workloads.
{: .fs-6 .fw-300 }

## Table of contents
{: .no_toc .text-delta }

1. TOC
{:toc}

---

## Overview

The Nightjar admission webhook is an **optional** component that runs as a separate Deployment from the controller. When developers create or update workloads, the webhook checks the controller's constraint index and returns **admission warnings** for any matching policies.

Key design principles:

- **Never rejects requests** — every admission response sets `Allowed: true`
- **Fail-open** — if the controller is unreachable or the webhook errors, the request is allowed silently
- **Separate binary** — isolates the admission path from the controller so a controller crash never blocks deployments
- **Warnings only** — uses the Kubernetes [admission response warnings](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#response) mechanism (not enforcement)

---

## How It Works

```
Developer runs: kubectl apply -f deployment.yaml
         │
         ▼
┌──────────────────────────┐
│   Kubernetes API Server  │
│   (admission chain)      │
└──────────┬───────────────┘
           │  AdmissionReview (POST /validate)
           ▼
┌──────────────────────────┐
│   Nightjar Webhook       │
│   (separate Deployment)  │
└──────────┬───────────────┘
           │  GET /api/v1/constraints?namespace=<ns>
           ▼
┌──────────────────────────┐
│   Nightjar Controller    │
│   (constraint index)     │
└──────────┬───────────────┘
           │  matching constraints
           ▼
┌──────────────────────────┐
│   Webhook builds         │
│   warning messages       │
│   Allowed: true          │
└──────────────────────────┘
           │
           ▼
   kubectl output shows warnings
```

1. The API server sends an `AdmissionReview` to the webhook's `/validate` endpoint
2. The webhook extracts the namespace and labels from the request
3. It queries the controller's HTTP API with a **3-second timeout**
4. Matching constraints with `Warning` or `Critical` severity are formatted as admission warnings
5. The response always allows the request (`Allowed: true`)

---

## Deployment

The webhook runs as a separate Kubernetes Deployment with its own Service and ValidatingWebhookConfiguration.

### Enabling

```yaml
# values.yaml
admissionWebhook:
  enabled: true    # default
  replicas: 2
```

### Architecture

| Component | Description |
|-----------|-------------|
| Deployment | Runs the `/webhook` binary, 2 replicas by default |
| Service | ClusterIP on port 443 (targets container port 8443) |
| ValidatingWebhookConfiguration | Registers the webhook with the API server |
| PodDisruptionBudget | `minAvailable: 1` to maintain availability during rollouts |

The webhook pods use pod anti-affinity to spread across nodes, and run with a hardened security context:

- `readOnlyRootFilesystem: true`
- `runAsNonRoot: true`
- All capabilities dropped

---

## Fail-Open Guarantee

{: .warning }
> The `failurePolicy` must **always** be `Ignore`. Setting it to `Fail` would cause Nightjar to block all deployments when the webhook is unavailable.

The webhook is designed to never interfere with cluster operations:

| Scenario | Behavior |
|----------|----------|
| Controller unreachable | Request allowed, no warnings |
| Constraint query times out (3s) | Request allowed, no warnings |
| Invalid admission request body | Request allowed, no warnings |
| Webhook pod crashes | API server skips webhook (`failurePolicy: Ignore`) |
| Webhook returns error | API server ignores it |

Every code path in the admission handler returns `Allowed: true`. There is no reject path.

---

## Watched Resources

The webhook intercepts **CREATE** and **UPDATE** operations on these resources:

| API Group | Resources |
|-----------|-----------|
| `""` (core) | pods, services, configmaps |
| `apps` | deployments, statefulsets, daemonsets, replicasets |
| `batch` | jobs, cronjobs |

Only **namespaced** resources are watched (`scope: Namespaced`).

### Excluded Namespaces

The webhook skips these namespaces entirely via `namespaceSelector`:

- `kube-system`
- `kube-public`
- `kube-node-lease`
- The webhook's own namespace (prevents recursive admission loops)
- Any namespaces listed in `admissionWebhook.excludedNamespaces`

---

## How Warnings Appear

When a developer deploys a workload that matches active constraints, `kubectl` displays warnings inline:

```
$ kubectl apply -f deployment.yaml
Warning: [WARNING] Egress to port 443 is denied by default-deny-egress - Add a NetworkPolicy allowing egress to port 443
Warning: [CRITICAL] Resource quota CPU limit exceeded in namespace production - Request a quota increase from the platform team
deployment.apps/my-app configured
```

The warning format is:

```
[WARNING] <summary> - <remediation hint>
[CRITICAL] <summary> - <remediation hint>
```

Only constraints with `Warning` or `Critical` severity generate warnings. `Info`-level constraints are excluded to reduce noise.

---

## Certificate Management

The webhook requires TLS certificates. Two modes are supported:

### Self-Signed (Default)

```yaml
admissionWebhook:
  certManagement: self-signed
```

In self-signed mode, the webhook manages its own CA and server certificates:

- Generates a 2048-bit RSA CA certificate and server certificate
- Certificates are valid for **1 year**
- Stored in a Kubernetes Secret (`nightjar-webhook-tls`, type `kubernetes.io/tls`)
- Server certificate includes DNS SANs for in-cluster service discovery:
  - `nightjar-webhook`
  - `nightjar-webhook.<namespace>`
  - `nightjar-webhook.<namespace>.svc`
  - `nightjar-webhook.<namespace>.svc.cluster.local`
- A background watcher checks every **24 hours** and rotates certificates that expire within **30 days**
- After rotation, the CA bundle in the ValidatingWebhookConfiguration is updated automatically
- Certificates are loaded dynamically (hot-reload) so rotation does not require a pod restart

### cert-manager

```yaml
admissionWebhook:
  certManagement: cert-manager
```

In cert-manager mode:

- cert-manager creates and manages the TLS secret
- The ValidatingWebhookConfiguration is annotated with `cert-manager.io/inject-ca-from` for automatic CA bundle injection
- Certificate files are mounted from the secret at `/etc/webhook/certs`
- The webhook reads `tls.crt` and `tls.key` from the mounted volume

This mode requires cert-manager to be installed in the cluster with a configured issuer.

---

## Configuration

### Helm Values

| Parameter | Default | Description |
|-----------|---------|-------------|
| `admissionWebhook.enabled` | `true` | Deploy the webhook |
| `admissionWebhook.replicas` | `2` | Number of webhook replicas |
| `admissionWebhook.failurePolicy` | `Ignore` | Webhook failure behavior (**never change**) |
| `admissionWebhook.timeoutSeconds` | `5` | API server timeout for webhook calls |
| `admissionWebhook.certManagement` | `self-signed` | Certificate strategy: `self-signed` or `cert-manager` |
| `admissionWebhook.pdb.enabled` | `true` | Enable PodDisruptionBudget |
| `admissionWebhook.pdb.minAvailable` | `1` | Minimum available pods |
| `admissionWebhook.excludedNamespaces` | `[]` | Additional namespaces to exclude |
| `admissionWebhook.resources.requests.cpu` | `50m` | CPU request |
| `admissionWebhook.resources.requests.memory` | `128Mi` | Memory request |
| `admissionWebhook.resources.limits.cpu` | `200m` | CPU limit |
| `admissionWebhook.resources.limits.memory` | `256Mi` | Memory limit |

### CLI Flags

The webhook binary accepts these flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:8443` | Listen address |
| `--controller-url` | `http://nightjar-controller.nightjar-system.svc:8080` | Controller API endpoint |
| `--namespace` | `nightjar-system` | Namespace where the webhook runs |
| `--self-signed` | `true` | Use self-signed certificate management |
| `--tls-cert-file` | `""` | Path to TLS certificate file (cert-manager mode) |
| `--tls-key-file` | `""` | Path to TLS key file (cert-manager mode) |

---

## Health Endpoints

| Endpoint | Port | Scheme | Description |
|----------|------|--------|-------------|
| `/healthz` | 8443 | HTTPS | Liveness probe (initial delay 10s, period 10s) |
| `/readyz` | 8443 | HTTPS | Readiness probe (initial delay 5s, period 5s) |

---

## Interaction with the Controller

The webhook and controller are decoupled via HTTP:

```
Webhook  ──GET /api/v1/constraints?namespace=<ns>──▶  Controller
         ◀──JSON { constraints: [...] }──────────────
```

- The webhook queries the controller on each admission request
- The query includes the workload's namespace; the controller returns matching constraints
- A **3-second context timeout** is applied to each query; the HTTP client has a 5-second fallback timeout
- If the controller is unavailable, the webhook fails open (allows the request, no warnings)
- The HTTP client uses connection pooling (10 idle connections per host, 90s idle timeout)

---

## Troubleshooting

### Webhook not returning warnings

1. Verify the webhook is registered:
   ```bash
   kubectl get validatingwebhookconfigurations | grep nightjar
   ```

2. Check the webhook pods are running:
   ```bash
   kubectl get pods -n nightjar-system -l app.kubernetes.io/component=webhook
   ```

3. Check webhook logs for query errors:
   ```bash
   kubectl logs -n nightjar-system -l app.kubernetes.io/component=webhook
   ```

4. Verify the controller API is reachable from the webhook:
   ```bash
   kubectl exec -n nightjar-system deploy/nightjar-webhook -- \
     wget -qO- http://nightjar-controller.nightjar-system.svc:8080/api/v1/health
   ```

### Certificate errors

1. Check the TLS secret exists:
   ```bash
   kubectl get secret -n nightjar-system nightjar-webhook-tls
   ```

2. Verify certificate validity:
   ```bash
   kubectl get secret -n nightjar-system nightjar-webhook-tls \
     -o jsonpath='{.data.tls\.crt}' | base64 -d | \
     openssl x509 -noout -dates
   ```

3. Check the CA bundle is set in the webhook configuration:
   ```bash
   kubectl get validatingwebhookconfigurations nightjar-webhook \
     -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | wc -c
   ```
   A non-zero value means the CA bundle is present.

### Webhook timing out

- The API server timeout is configured via `admissionWebhook.timeoutSeconds` (default: 5s)
- The internal controller query timeout is 3 seconds
- If the controller is slow, check controller pod resources and constraint count
- Increase `admissionWebhook.timeoutSeconds` if needed (but keep it under 10s to avoid slowing deployments)

## E2E Testing

The webhook is covered by E2E tests in `test/e2e/webhook_test.go`. To run them:

```bash
make e2e-setup   # Deploys controller + webhook in Kind with self-signed certs
make e2e         # Runs all E2E tests including webhook suite
make e2e-teardown
```

The E2E setup enables the webhook with `admissionWebhook.enabled=true`, `replicas=2`, and `certManagement=self-signed`. Tests verify:

- Webhook deployment readiness and service endpoints
- Health probe liveness (via pod Ready condition)
- Admission warnings with `[WARNING]`/`[CRITICAL]` prefixes for matching constraints
- Never-reject guarantee (workloads always admitted)
- Fail-open behavior when the webhook is unavailable
- Self-signed TLS certificate injection (Secret + VWC caBundle)
- PodDisruptionBudget enforcement (minAvailable when replicas > 1)
