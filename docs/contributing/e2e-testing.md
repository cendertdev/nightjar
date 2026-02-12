---
layout: default
title: E2E Testing
parent: Contributing
nav_order: 1
---

# E2E Testing
{: .no_toc }

Run end-to-end tests against a real Kubernetes cluster.
{: .fs-6 .fw-300 }

## Table of contents
{: .no_toc .text-delta }

1. TOC
{:toc}

---

## Overview

E2E tests validate Nightjar's full lifecycle against a real cluster: constraint
discovery, event emission, workload annotation, and controller health. They run
separately from unit and integration tests via the `//go:build e2e` tag.

Two cluster backends are supported:

| Backend | Isolation | Extra Install | Use Case |
|---|---|---|---|
| **Docker Desktop K8s** | Shared cluster | None | Quick iteration, WSL2 friendly |
| **Kind** | Disposable cluster | `kind` CLI | CI, clean-room testing |

---

## Docker Desktop (Recommended for Local Dev)

Docker Desktop's built-in Kubernetes shares the local Docker daemon, so
locally-built images are available without any image-loading step.

### Prerequisites

- Docker Desktop with **Kubernetes enabled** (Settings > Kubernetes > Enable)
- `kubectl`, `helm`, Go 1.25+

### Workflow

```bash
# Verify your cluster is running
kubectl cluster-info

# Build images, install CRDs, deploy controller
make e2e-setup-dd

# Run the tests
make e2e

# Clean up (removes only Nightjar resources)
make e2e-teardown-dd
```

{: .note }
`e2e-teardown-dd` only removes Nightjar's Helm release, CRDs, and
test namespaces. Your other workloads are not affected.

### What Happens

1. **`make e2e-setup-dd`** builds controller and webhook Docker images, applies
   CRDs, and deploys the controller via Helm with simplified settings (1 replica,
   no leader election, no webhook, `pullPolicy=Never`).
2. **`make e2e`** runs `go test ./test/e2e/... -tags=e2e -timeout 30m`. The test
   suite connects via your kubeconfig, creates a labeled namespace, verifies the
   controller is healthy, runs tests, and cleans up.
3. **`make e2e-teardown-dd`** runs `helm uninstall`, deletes CRDs, and removes
   any leftover test namespaces (`nightjar-e2e=true` label).

---

## Kind (Isolated Cluster)

Kind creates a full Kubernetes cluster inside Docker containers. Everything is
destroyed on teardown.

### Prerequisites

- Docker, `kubectl`, `helm`, Go 1.25+
- [Kind](https://kind.sigs.k8s.io/): `go install sigs.k8s.io/kind@latest`

{: .warning }
**WSL2 users:** Kind requires cgroup v2. If `cat /sys/fs/cgroup/cgroup.controllers`
fails, add `kernelCommandLine = cgroup_no_v1=all systemd.unified_cgroup_hierarchy=1`
to `%USERPROFILE%\.wslconfig` and run `wsl --shutdown`. Or use Docker Desktop
Kubernetes instead.

### Workflow

```bash
# Build images, create Kind cluster, load images, deploy controller
make e2e-setup

# Run the tests
make e2e

# Tear down (deletes the entire Kind cluster)
make e2e-teardown
```

---

## Writing E2E Tests

E2E test files live in `test/e2e/` and must include the build tag:

```go
//go:build e2e
// +build e2e
```

### Test Suite

Tests use [testify suites](https://pkg.go.dev/github.com/stretchr/testify/suite).
The `E2ESuite` in `suite_test.go` provides:

| Field | Type | Description |
|---|---|---|
| `clientset` | `kubernetes.Interface` | Standard Kubernetes client |
| `dynamicClient` | `dynamic.Interface` | For unstructured objects |
| `namespace` | `string` | Test namespace (created per run) |

Add new tests as methods on `E2ESuite`:

```go
func (s *E2ESuite) TestMyFeature() {
    t := s.T()
    // Use s.clientset, s.dynamicClient, s.namespace
}
```

### Available Helpers

All helpers are in `helpers_test.go`:

| Helper | Purpose |
|---|---|
| `waitForCondition(t, timeout, interval, fn)` | Generic polling loop |
| `waitForControllerReady(t, clientset, timeout)` | Wait for controller deployment |
| `waitForDeploymentReady(t, clientset, ns, name, timeout)` | Wait for any deployment |
| `createTestNamespace(t, clientset)` | Create labeled namespace + cleanup func |
| `deleteNamespace(t, clientset, name, timeout)` | Delete and wait for removal |
| `waitForEvent(t, clientset, ns, objectName, timeout)` | Poll for K8s Events on an object |
| `assertEventExists(t, clientset, ns, workload, annotations, timeout)` | Assert Nightjar event with annotations |
| `assertEventAnnotation(t, event, key, value)` | Check single annotation |
| `assertManagedByNightjar(t, event)` | Check `nightjar.io/managed-by` |
| `applyUnstructured(t, dynamicClient, obj)` | Create an unstructured object |
| `deleteUnstructured(t, dynamicClient, gvr, ns, name)` | Delete an unstructured object |
| `getControllerLogs(t, clientset, tailLines)` | Retrieve controller pod logs |
| `getConstraintReport(t, dynClient, ns, timeout)` | Poll for ConstraintReport "constraints" via dynamic client |
| `getReportStatus(report)` | Extract `.status` map from unstructured report (nil-safe) |
| `waitForReportCondition(t, dynClient, ns, timeout, condFn)` | Poll until condition is true on report status |
| `statusInt64(status, key)` | Safely extract int64 from status map field |
| `statusConstraintNames(status)` | Extract constraint names from `status.constraints[]` |
| `statusConstraintSources(status)` | Extract constraint sources from `status.constraints[]` |
| `createTestDeployment(t, dynamicClient, ns, name)` | Create a pause:3.9 Deployment + cleanup func |
| `waitForNightjarEvent(t, clientset, ns, workload, timeout)` | Poll for ConstraintNotification Events from nightjar-controller |
| `getNightjarEvents(t, clientset, ns, workload)` | List Nightjar events (non-waiting, for counting) |
| `createWarningEvent(t, clientset, ns, involved, kind)` | Create a synthetic Warning event for correlation testing |
| `waitForWorkloadAnnotation(t, dynClient, ns, deploy, key, timeout)` | Poll until a workload annotation is present |

### Example: Testing a NetworkPolicy Constraint

```go
func (s *E2ESuite) TestNetworkPolicyDiscovery() {
    t := s.T()

    // Create a NetworkPolicy in the test namespace
    np := &unstructured.Unstructured{
        Object: map[string]interface{}{
            "apiVersion": "networking.k8s.io/v1",
            "kind":       "NetworkPolicy",
            "metadata": map[string]interface{}{
                "name":      "deny-all-egress",
                "namespace": s.namespace,
            },
            "spec": map[string]interface{}{
                "podSelector": map[string]interface{}{},
                "policyTypes": []interface{}{"Egress"},
            },
        },
    }
    applyUnstructured(t, s.dynamicClient, np)

    // Wait for Nightjar to discover and emit an event
    assertEventExists(t, s.clientset, s.namespace, "deny-all-egress",
        map[string]string{
            annotations.ManagedBy:       annotations.ManagedByValue,
            annotations.EventConstraintType: "NetworkEgress",
        },
        30*time.Second,
    )
}
```

### Constraint Report Tests

`constraint_report_test.go` verifies the indexer → report reconciler pipeline:

| Test | Verifies |
|---|---|
| `TestConstraintReportCreatedOnConstraint` | Creating a NetworkPolicy triggers a ConstraintReport with correct counts and machineReadable |
| `TestConstraintReportUpdateOnConstraintChange` | Updating a ResourceQuota re-reconciles the report (lastUpdated changes) |
| `TestConstraintReportDeleteConstraint` | Deleting a constraint removes it from the report (by name) |
| `TestConstraintReportMachineReadable` | machineReadable section has schemaVersion, detailLevel, structured entries with UID/SourceRef/Remediation |
| `TestConstraintReportSeverityCounts` | Multiple constraints produce correct severity counts (Warning + Info) |
| `TestConstraintReportClusterScopedConstraint` | Cluster-scoped webhook appears in the test namespace's report |

These tests use `waitForReportCondition` with timeouts of 60s (create) and 45s
(update) to account for the debounce + ticker + reconcile pipeline latency.

### Correlation & Notification Tests

`correlation_test.go` verifies the event correlation engine and notification pipeline:

| Test | Verifies |
|---|---|
| `TestCorrelationEventCreated` | Warning event → Correlator → Dispatcher → ConstraintNotification Event with structured annotations |
| `TestCorrelationDeduplication` | Same constraint-workload pair does not produce duplicate Events within the suppression window |
| `TestCorrelationPrivacyScoping` | Cross-namespace constraint Events use summary-level privacy (name redacted, no cross-NS details) |
| `TestWorkloadAnnotationPatched` | Deployment receives `nightjar.io/status`, `nightjar.io/constraints` JSON, severity counts |
| `TestCorrelationRateLimiting` | Burst Warning events are throttled by the per-namespace rate limiter |

These tests create a constraint first, wait for it to be indexed (via ConstraintReport),
then create synthetic Warning events to trigger the correlation pipeline. Timeouts
account for the full pipeline: informer sync + adapter parse + indexer upsert +
event watch + correlation + dispatch.

---

## Makefile Target Reference

| Target | Backend | Description |
|---|---|---|
| `make e2e-setup-dd` | Docker Desktop | Build images, install CRDs, deploy controller |
| `make e2e-teardown-dd` | Docker Desktop | Uninstall release, CRDs, test namespaces |
| `make e2e-setup` | Kind | Create cluster, build/load images, deploy |
| `make e2e-teardown` | Kind | Delete Kind cluster entirely |
| `make e2e` | Any | Run E2E tests (`go test -tags=e2e -timeout 30m`) |
| `make test-e2e` | Any | Same as `make e2e` |

---

## Troubleshooting

### Tests fail with "failed to load kubeconfig"

Ensure `KUBECONFIG` is set or `~/.kube/config` exists. For Kind:

```bash
kind export kubeconfig --name nightjar
```

### Controller never becomes ready

```bash
kubectl get pods -n nightjar-system
kubectl describe pod -n nightjar-system -l app.kubernetes.io/component=controller
kubectl logs -n nightjar-system -l app.kubernetes.io/component=controller
```

### Image pull errors

**Docker Desktop:** Verify the image exists locally:

```bash
docker images | grep nightjar
```

**Kind:** Ensure images are loaded:

```bash
kind load docker-image ghcr.io/cendertdev/nightjar:dev --name nightjar
```

### RBAC errors in controller logs

The Helm chart's RBAC grants cluster-wide read access. If you see `forbidden`
errors for patch operations on workloads in other namespaces, this is expected
on a shared cluster — the service account only has patch access to namespaces
where Nightjar is deployed. E2E tests use their own labeled namespace.
