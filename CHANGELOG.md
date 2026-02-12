# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- E2E tests for correlation engine and Event notifications — event creation with structured annotations, deduplication, privacy scoping, workload annotation patching, rate limiting
- Wire EventBuilder into Dispatcher for structured annotations on all emitted Events
- E2E tests for constraint indexer and ConstraintReport reconciliation — CRUD lifecycle, severity counting, machine-readable validation, cluster-scoped constraint propagation
- E2E test infrastructure and harness for Kind cluster testing — shared helpers, testify suite, Makefile targets (`e2e-setup`, `e2e`, `e2e-teardown`)
- Hubble flow drop streaming — connects to Hubble Relay and streams `verdict=DROPPED` flows for real-time network policy correlation
- Generic adapter field-path configuration — ConstraintProfile `fieldPaths` enables custom extraction of selectors, namespace selectors, effects, and summaries from arbitrary CRD schemas
- ConstraintProfile controller — controller-runtime reconciler for immediate profile registration/unregistration (no rescan delay)
- CRD annotation discovery — CRDs annotated with `nightjar.io/is-policy: "true"` are automatically treated as constraint sources
- Discovery tuning — configurable `additionalPolicyGroups`, `additionalPolicyNameHints`, and `checkCRDAnnotations` flags for heuristic customization
- Dynamic adapter registry — `Unregister`, `RegisterGVR`, and `UnregisterGVR` methods for runtime profile-driven adapter management
- Example ConstraintProfiles for cert-manager, Crossplane, and Argo Rollouts

### Fixed

- Hubble client: fix data race on connection field during shutdown
- Hubble client: use signal-handler context instead of background context for lifecycle management
- Hubble client: fix incorrect drop reason mappings and replace magic integers with proto enum constants
- Hubble client: add explicit ICMPv6 flow handling
- Hubble client: move DropReason.String() to production code
- Hubble client: fix reconnect counter to increment on stream disconnect, not after backoff timer
- Hubble client: deep-copy label maps and workload slices in FlowDropBuilder.Build()

## [0.1.0] - 2026-02-10

### Added

- Core controller with leader election and CRD rescan
- Discovery engine with automatic CRD scanning and heuristic detection
- Constraint adapters: NetworkPolicy, ResourceQuota, LimitRange, WebhookConfiguration, Cilium, Gatekeeper, Kyverno, Generic
- Adapter auto-detection (`auto` mode enables adapters when CRDs are present)
- In-memory constraint indexer with namespace/label/type queries
- Event correlator for matching Kubernetes events to constraints
- Missing resource detection (ServiceMonitor, VirtualService, PeerAuthentication) with configurable debounce
- Admission webhook (fail-open, warning mode) as separate deployment
- ConstraintReport CRD with human-readable and machine-readable sections
- ConstraintProfile CRD for custom CRD registration and adapter tuning
- NotificationPolicy CRD for privacy-scoped notification configuration
- Notification dispatcher: Kubernetes Events, ConstraintReports, Slack, generic webhooks
- Notification deduplication and rate limiting
- Privacy model with developer/platform-admin scoping and detail levels
- Workload annotation with constraint summaries
- MCP server for AI agent integration (SSE and stdio transports)
- HTTP API server (`/api/v1/health`, `/api/v1/capabilities`, `/openapi/v3`)
- CLI (`nightjar query`, `nightjar explain`, `nightjar check`, `nightjar status`)
- kubectl plugin (`kubectl sentinel`)
- Optional Hubble gRPC integration for real-time flow drop detection
- Prometheus metrics and optional ServiceMonitor/Grafana dashboard
- Helm chart with comprehensive values.yaml
- Documentation: architecture, quickstart, configuration reference, adapter guide, privacy model, CRD reference

[Unreleased]: https://github.com/cendertdev/nightjar/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/cendertdev/nightjar/releases/tag/v0.1.0
