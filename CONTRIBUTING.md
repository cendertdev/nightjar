# Contributing to Nightjar

Thank you for your interest in contributing! This project is in early development and contributions are welcome.

## Development Setup

### Prerequisites

- Go 1.22+
- Docker
- kubectl
- Kind (for local testing)
- Helm 3
- controller-gen (`go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest`)

### Getting Started

```bash
# Clone the repo
git clone https://github.com/nightjarctl/nightjar.git
cd nightjar

# Install dependencies
go mod download

# Run tests
make test

# Build the binary
make build

# Create a local Kind cluster
make kind-create

# Build and load the image
make kind-load

# Install via Helm
make helm-install
```

### Running Locally (Out-of-Cluster)

```bash
# Uses your current kubeconfig context
make run
```

## Project Structure

```
nightjar/
├── api/v1alpha1/           # CRD type definitions
├── cmd/
│   ├── controller/         # Main controller entrypoint
│   └── webhook/            # Admission webhook entrypoint
├── config/
│   ├── crd/                # Generated CRD manifests
│   ├── rbac/               # RBAC manifests
│   └── samples/            # Example CR instances
├── deploy/helm/            # Helm chart
├── docs/                   # Documentation
├── internal/
│   ├── adapters/           # Constraint adapters (one package per engine)
│   │   ├── registry.go     # Adapter registry
│   │   ├── networkpolicy/  # Native NetworkPolicy adapter
│   │   ├── cilium/         # Cilium adapter
│   │   ├── gatekeeper/     # Gatekeeper adapter
│   │   ├── kyverno/        # Kyverno adapter
│   │   ├── istio/          # Istio adapter
│   │   ├── resourcequota/  # ResourceQuota/LimitRange adapter
│   │   ├── webhook/        # ValidatingWebhookConfiguration adapter
│   │   └── generic/        # Fallback adapter for unknown CRDs
│   ├── correlator/         # Failure-to-constraint correlation
│   ├── discovery/          # CRD discovery engine
│   ├── hubble/             # Hubble flow client
│   ├── indexer/            # In-memory constraint index
│   ├── notifier/           # Notification dispatcher
│   ├── requirements/       # Missing resource detection
│   └── types/              # Shared types (Constraint, Adapter interface, etc.)
├── hack/                   # Scripts for development
└── test/
    ├── fixtures/           # Shared test YAML fixtures
    ├── integration/        # envtest-based integration tests
    └── e2e/                # Kind-based end-to-end tests
```

## Writing an Adapter

See [docs/ADAPTER_GUIDE.md](docs/ADAPTER_GUIDE.md) for detailed instructions on writing a new constraint adapter.

The short version:

1. Create a package under `internal/adapters/youradapter/`
2. Implement the `types.Adapter` interface
3. Register it in `cmd/controller/main.go`
4. Write tests with YAML fixtures in `testdata/`
5. Submit a PR

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `golangci-lint` (config in `.golangci.yml`)
- Parse Kubernetes objects from `unstructured.Unstructured`, not typed clients
- Handle missing fields defensively — CRD schemas change across versions
- Write clear `Summary` strings — they appear in developer notifications
- Every adapter needs comprehensive test fixtures

## Pull Request Process

1. Fork the repo and create a branch from `main`
2. Add tests for any new functionality
3. Run `make test` and `make lint` — both must pass
4. Update documentation if you're changing behavior
5. Write a clear PR description explaining what and why
6. One approval required for merge

## Reporting Issues

- Use GitHub Issues
- Include: Kubernetes version, installed policy engines, steps to reproduce
- For constraint discovery issues: include the CRD YAML and expected behavior
