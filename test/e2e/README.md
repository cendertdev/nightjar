# E2E Tests

End-to-end tests run against a real Kubernetes cluster to validate Nightjar's
full lifecycle: constraint discovery, event emission, and workload annotation.

## Prerequisites

- [Docker](https://www.docker.com/products/docker-desktop/) (for building images)
- [Kind](https://kind.sigs.k8s.io/) (`go install sigs.k8s.io/kind@latest`)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/) v3+
- Go 1.25+

## Quick Start

```bash
# 1. Build images, create Kind cluster, install CRDs, deploy controller
make e2e-setup

# 2. Run E2E tests
make e2e

# 3. Tear down (deletes Kind cluster and all resources)
make e2e-teardown
```

## What `make e2e-setup` Does

1. Builds controller and webhook Docker images (`docker-build-all`)
2. Installs CRDs into the cluster (`kubectl apply -f config/crd/`)
3. Creates a Kind cluster named `nightjar` (if it doesn't already exist)
4. Loads Docker images into the Kind cluster
5. Deploys the controller via Helm with simplified settings:
   - 1 replica (no HA needed for testing)
   - Leader election disabled
   - Admission webhook disabled (avoids certificate complexity)
   - Image pull policy `IfNotPresent` (uses locally loaded image)
6. Waits up to 120s for the deployment to become ready

## What `make e2e-teardown` Does

1. Uninstalls the Helm release
2. Deletes CRDs from the cluster
3. Deletes all test namespaces (labeled `nightjar-e2e=true`)
4. Deletes the Kind cluster

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

## Docker Desktop Alternative

If you prefer Docker Desktop's built-in Kubernetes over Kind:

1. Enable Kubernetes in Docker Desktop settings
2. Verify: `kubectl cluster-info`
3. Build images: `make docker-build-all`
4. Install CRDs: `make install`
5. Deploy with local images:
   ```bash
   helm upgrade --install nightjar deploy/helm/ \
     --namespace nightjar-system \
     --create-namespace \
     --set controller.replicas=1 \
     --set controller.leaderElect=false \
     --set controller.image.tag=dev \
     --set controller.image.pullPolicy=Never \
     --set admissionWebhook.enabled=false \
     --wait --timeout 120s
   ```
   Note: `pullPolicy=Never` works because Docker Desktop Kubernetes shares the
   local Docker daemon.
6. Run tests: `make e2e`
7. Cleanup:
   ```bash
   helm uninstall nightjar -n nightjar-system
   kubectl delete -f config/crd/
   kubectl delete ns -l nightjar-e2e=true
   ```

## Troubleshooting

**Tests fail with "failed to load kubeconfig"**
- Ensure `KUBECONFIG` points to a valid file, or `~/.kube/config` exists
- For Kind: `kind export kubeconfig --name nightjar`

**Controller never becomes ready**
- Check pod status: `kubectl get pods -n nightjar-system`
- Check pod events: `kubectl describe pod -n nightjar-system -l app.kubernetes.io/component=controller`
- Check logs: `kubectl logs -n nightjar-system -l app.kubernetes.io/component=controller`

**Image pull errors**
- Ensure images are loaded into Kind: `kind load docker-image ghcr.io/cendertdev/nightjar:dev --name nightjar`
- Verify: `docker exec nightjar-control-plane crictl images | grep nightjar`

**Stale test namespaces**
- Clean up: `kubectl delete ns -l nightjar-e2e=true`
