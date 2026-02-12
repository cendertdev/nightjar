# E2E Tests

End-to-end tests run against a real Kubernetes cluster to validate Nightjar's
full lifecycle: constraint discovery, event emission, and workload annotation.

Two cluster backends are supported: **Kind** (isolated, disposable) and
**Docker Desktop Kubernetes** (shared, no extra install).

## Prerequisites

- [Docker](https://www.docker.com/products/docker-desktop/) (for building images)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/) v3+
- Go 1.25+

**Kind only** (optional):
- [Kind](https://kind.sigs.k8s.io/) (`go install sigs.k8s.io/kind@latest`)
- WSL2 users: Kind requires cgroup v2. See [Troubleshooting](#kind-fails-on-wsl2).

## Quick Start — Docker Desktop

Docker Desktop's built-in Kubernetes shares the local Docker daemon, so
locally-built images are available immediately (`pullPolicy=Never`).

```bash
# 1. Enable Kubernetes in Docker Desktop settings, then verify:
kubectl cluster-info

# 2. Build images, install CRDs, deploy controller
make e2e-setup-dd

# 3. Run E2E tests
make e2e

# 4. Tear down (removes Nightjar, preserves your other workloads)
make e2e-teardown-dd
```

## Quick Start — Kind

Kind creates a disposable cluster in Docker containers. Complete isolation,
clean teardown.

```bash
# 1. Build images, create Kind cluster, install CRDs, deploy controller
make e2e-setup

# 2. Run E2E tests
make e2e

# 3. Tear down (deletes the entire Kind cluster)
make e2e-teardown
```

## Makefile Targets

| Target | Backend | Description |
|---|---|---|
| `make e2e-setup-dd` | Docker Desktop | Build images, install CRDs, deploy controller |
| `make e2e-teardown-dd` | Docker Desktop | Uninstall Helm release, CRDs, test namespaces |
| `make e2e-setup` | Kind | Create Kind cluster, build/load images, install CRDs, deploy |
| `make e2e-teardown` | Kind | Delete Kind cluster and all resources |
| `make e2e` | Any | Run E2E tests (alias for `make test-e2e`) |

### What `make e2e-setup-dd` Does

1. Builds controller and webhook Docker images (`docker-build-all`)
2. Installs CRDs (`kubectl apply -f config/crd/`)
3. Deploys the controller via Helm with simplified settings:
   - 1 replica, leader election disabled, webhook disabled
   - `pullPolicy=Never` (uses locally built image)
4. Waits up to 120s for the deployment to become ready

### What `make e2e-setup` Does (Kind)

1. Builds controller and webhook Docker images (`docker-build-all`)
2. Creates a Kind cluster named `nightjar` (if it doesn't already exist)
3. Installs CRDs into the Kind cluster
4. Loads Docker images into the Kind cluster
5. Deploys the controller via Helm with simplified settings:
   - 1 replica, leader election disabled, webhook disabled
   - `pullPolicy=IfNotPresent` (uses Kind-loaded image)
6. Waits up to 120s for the deployment to become ready

## Test Structure

```
test/e2e/
  suite_test.go      # Test suite with setup/teardown
  helpers_test.go     # Shared helper functions
  README.md           # This file
```

All files use the `//go:build e2e` tag, so they are excluded from `make test`.

## Configuration

| Environment Variable | Default | Description |
|---|---|---|
| `KUBECONFIG` | `~/.kube/config` | Path to kubeconfig file. Kind sets this automatically. |

The test suite uses `controller-runtime`'s config resolution, which checks
`KUBECONFIG`, then `~/.kube/config`, then in-cluster config.

## Timeouts

- **`make e2e`**: 30-minute overall timeout (`-timeout 30m`)
- **Controller readiness**: 120s wait in `SetupSuite`
- **Individual wait helpers**: configurable per call, default 60s

## Troubleshooting

**Tests fail with "failed to load kubeconfig"**
- Ensure `KUBECONFIG` points to a valid file, or `~/.kube/config` exists
- For Kind: `kind export kubeconfig --name nightjar`

**Controller never becomes ready**
- Check pod status: `kubectl get pods -n nightjar-system`
- Check pod events: `kubectl describe pod -n nightjar-system -l app.kubernetes.io/component=controller`
- Check logs: `kubectl logs -n nightjar-system -l app.kubernetes.io/component=controller`

**Image pull errors (Docker Desktop)**
- Ensure `pullPolicy=Never` is set (the `e2e-setup-dd` target handles this)
- Verify image exists: `docker images | grep nightjar`

**Image pull errors (Kind)**
- Ensure images are loaded: `kind load docker-image ghcr.io/cendertdev/nightjar:dev --name nightjar`
- Verify: `docker exec nightjar-control-plane crictl images | grep nightjar`

**Kind fails on WSL2**
- Kind requires cgroup v2. Check: `cat /sys/fs/cgroup/cgroup.controllers`
- If you see "No such file or directory", add to `%USERPROFILE%\.wslconfig`:
  ```
  [wsl2]
  kernelCommandLine = cgroup_no_v1=all systemd.unified_cgroup_hierarchy=1
  ```
- Then: `wsl --shutdown` and restart
- Or use Docker Desktop Kubernetes instead: `make e2e-setup-dd`

**Stale test namespaces**
- Clean up: `kubectl delete ns -l nightjar-e2e=true`
