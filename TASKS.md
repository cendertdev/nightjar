# TASKS.md

Tasks are ordered. Work top to bottom. Each task is atomic. Every task has a verify command that exits 0 on success.

## Phase 0: Scaffolding

### TASK-0.1: Initialize Go module
- file: `go.mod`, `go.sum`
- verify: `go mod tidy && go build ./... 2>&1 | head -5`
- spec: |
    The existing `go.mod` is a placeholder. Replace it:
    ```
    go mod init github.com/nightjarctl/nightjar
    ```
    Then add dependencies:
    ```
    go get sigs.k8s.io/controller-runtime@v0.17.0
    go get k8s.io/client-go@v0.29.0
    go get k8s.io/apimachinery@v0.29.0
    go get go.uber.org/zap@v1.27.0
    go get github.com/prometheus/client_golang@v1.19.0
    go get github.com/stretchr/testify@v1.9.0
    ```
    Then fix ALL import paths in existing `.go` files so `go build ./...` passes.
    Known issues to fix:
    - `internal/adapters/networkpolicy/adapter.go` references `metav1` without importing it
    - `cmd/controller/main.go` has commented-out imports that may cause issues
    - `internal/util/unstructured.go` imports `metav1` — ensure path is correct
    Run `go mod tidy` at the end.

### TASK-0.2: Implement safe unstructured helpers
- file: `internal/util/unstructured.go`
- test: `internal/util/unstructured_test.go` (pre-written, make all tests pass)
- verify: `go test ./internal/util/ -v -count=1`
- spec: |
    Implement every function stub in `internal/util/unstructured.go`.
    Each has an `// IMPLEMENT:` comment. The pre-written test file defines
    exact expected behavior. Key behaviors:
    - All functions return zero values on missing fields (never error)
    - `SafeNestedLabelSelector` must parse both `matchLabels` and `matchExpressions`
    - `SafeNestedLabelSelector` returns nil (not empty struct) when field is missing

### TASK-0.3: Generate DeepCopy and CRD manifests
- file: `api/v1alpha1/zz_generated.deepcopy.go`, `api/v1alpha1/doc.go`, `config/crd/*.yaml`
- verify: `test -f api/v1alpha1/zz_generated.deepcopy.go && ls config/crd/*.yaml | wc -l | grep -q 3`
- spec: |
    1. Create `api/v1alpha1/doc.go` with:
       ```go
       // +kubebuilder:object:generate=true
       // +groupName=nightjar.io
       package v1alpha1
       ```
    2. Create `api/v1alpha1/groupversion_info.go` with SchemeBuilder and GroupVersion.
    3. Run `controller-gen object paths="./api/..."` → generates zz_generated.deepcopy.go
    4. Run `controller-gen crd paths="./api/..." output:crd:artifacts:config=config/crd`
    5. Verify: 3 CRD YAML files in config/crd/

### TASK-0.4: Fix main.go compilation
- file: `cmd/controller/main.go`
- verify: `go build ./cmd/controller/`
- spec: |
    The main.go has TODOs and nil clients. Wire real clients:
    ```go
    cfg := mgr.GetConfig()
    discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
    dynamicClient, err := dynamic.NewForConfig(cfg)
    ```
    Pass to `discovery.NewEngine()`. Comment out any subsystems that don't
    exist yet (correlator, notifier, etc.) but leave TODO comments.
    Goal: `go build ./cmd/controller/` exits 0.

### TASK-0.5: Create golangci-lint config
- file: `.golangci.yml`
- verify: `test -f .golangci.yml`
- spec: |
    ```yaml
    run:
      timeout: 5m
    linters:
      enable:
        - govet
        - errcheck
        - staticcheck
        - unused
        - gosimple
        - ineffassign
        - typecheck
        - misspell
        - gofmt
    issues:
      exclude-files:
        - "zz_generated.*"
    ```

### TASK-0.6: Create verification Makefile targets
- file: `Makefile` (append to existing)
- verify: `grep -q "verify-phase-0" Makefile`
- spec: |
    Add these targets to the Makefile:
    ```makefile
    .PHONY: verify
    verify: verify-phase-0 verify-phase-1

    .PHONY: verify-phase-0
    verify-phase-0:
    	@echo "=== Phase 0 Verification ==="
    	go build ./...
    	go test ./internal/util/ -v -count=1
    	test -f api/v1alpha1/zz_generated.deepcopy.go
    	@echo "=== Phase 0 PASSED ==="

    .PHONY: verify-phase-1
    verify-phase-1:
    	@echo "=== Phase 1 Verification ==="
    	go test ./internal/... -v -count=1
    	go test ./cmd/... -v -count=1
    	@echo "=== Phase 1 PASSED ==="
    ```

---

## Phase 1: Core Discovery + Native Adapters

### TASK-1.1: Implement constraint indexer
- file: `internal/indexer/indexer.go`
- test: `internal/indexer/indexer_test.go` (pre-written)
- contract: `internal/indexer/doc.go` (read first)
- verify: `go test ./internal/indexer/ -v -count=1`
- spec: |
    Concurrent-safe in-memory store of `types.Constraint` objects indexed by UID.
    Secondary indexes by namespace, constraint type, and source GVR.
    Methods (all defined in doc.go):
    - `Upsert(c Constraint)` — add or replace by UID
    - `Delete(uid types.UID)` — remove
    - `ByNamespace(ns string) []Constraint`
    - `ByLabels(ns string, labels map[string]string) []Constraint` — match WorkloadSelector
    - `ByType(ct ConstraintType) []Constraint`
    - `BySourceGVR(gvr schema.GroupVersionResource) []Constraint`
    - `All() []Constraint`
    - `Count() int`
    Use `sync.RWMutex`. Label matching uses `labels.SelectorFromSet()`.

### TASK-1.2: Complete NetworkPolicy adapter
- file: `internal/adapters/networkpolicy/adapter.go`
- test: `internal/adapters/networkpolicy/adapter_test.go` (pre-written)
- verify: `go test ./internal/adapters/networkpolicy/ -v -count=1`
- spec: |
    Finish the stub. Parse:
    1. `spec.podSelector` → `WorkloadSelector` via `util.SafeNestedLabelSelector`
    2. `spec.policyTypes` → determine ingress/egress/both
    3. `spec.ingress[].from[].podSelector`, `.namespaceSelector`, `.ipBlock`
    4. `spec.ingress[].ports[]` → port number + protocol
    5. `spec.egress[].to[]` and `spec.egress[].ports[]` — same pattern
    6. Build Summary: "Restricts egress to ports 443, 8443, 9090" or "Denies all ingress"
    7. Details map: `{"allowedPorts": [...], "ruleCount": N}`
    Test fixtures in testdata/ define exact expected outputs.

### TASK-1.3: Implement ResourceQuota adapter
- file: `internal/adapters/resourcequota/adapter.go`
- test: `internal/adapters/resourcequota/adapter_test.go` (pre-written)
- contract: `internal/adapters/resourcequota/doc.go`
- verify: `go test ./internal/adapters/resourcequota/ -v -count=1`
- spec: |
    Handles: `{"", "v1", "resourcequotas"}`
    Parse `spec.hard` and `status.used`. Compute percentage for each resource.
    One Constraint per ResourceQuota object with:
    - ConstraintType: ResourceLimit
    - Severity: Info (<75%), Warning (75-90%), Critical (>90%)
    - Summary: "CPU: 3.2/4 cores (80%); Memory: 6.1/8Gi (76%)"
    - Details: {"hard": {...}, "used": {...}, "percentUsed": {...}}
    - AffectedNamespaces: [obj.GetNamespace()]

### TASK-1.4: Implement LimitRange adapter
- file: `internal/adapters/limitrange/adapter.go`
- test: `internal/adapters/limitrange/adapter_test.go` (pre-written)
- contract: `internal/adapters/limitrange/doc.go`
- verify: `go test ./internal/adapters/limitrange/ -v -count=1`
- spec: |
    Handles: `{"", "v1", "limitranges"}`
    Parse `spec.limits[]`. Each entry has `type` (Container, Pod, PVC),
    `default`, `defaultRequest`, `max`, `min`, `maxLimitRequestRatio`.
    One Constraint per limit entry with:
    - ConstraintType: ResourceLimit
    - Severity: Info
    - Summary: "Container defaults: CPU 100m-500m, Memory 128Mi-512Mi"

### TASK-1.5: Implement webhook adapter
- file: `internal/adapters/webhookconfig/adapter.go`
- test: `internal/adapters/webhookconfig/adapter_test.go` (pre-written)
- contract: `internal/adapters/webhookconfig/doc.go`
- verify: `go test ./internal/adapters/webhookconfig/ -v -count=1`
- spec: |
    Handles BOTH:
    - `{"admissionregistration.k8s.io", "v1", "validatingwebhookconfigurations"}`
    - `{"admissionregistration.k8s.io", "v1", "mutatingwebhookconfigurations"}`
    Parse `webhooks[]`: extract `name`, `rules[].apiGroups`, `rules[].resources`,
    `rules[].operations`, `namespaceSelector`, `failurePolicy`, `sideEffects`.
    One Constraint per webhook entry (not per config — per individual webhook within).
    - ConstraintType: Admission
    - Severity: Warning if failurePolicy=Fail, Info if Ignore
    - Summary: "Validating webhook 'check-labels' intercepts CREATE,UPDATE on pods"
    NOTE: Skip webhooks owned by nightjar itself.

### TASK-1.6: Implement generic fallback adapter
- file: `internal/adapters/generic/adapter.go`
- test: `internal/adapters/generic/adapter_test.go` (pre-written)
- contract: `internal/adapters/generic/doc.go`
- verify: `go test ./internal/adapters/generic/ -v -count=1`
- spec: |
    This adapter is NOT registered to specific GVRs. Instead, the discovery
    engine uses it as a fallback when no specific adapter matches.
    Best-effort extraction:
    1. Check `metadata.annotations["nightjar.io/summary"]` — use as Summary if present
    2. Try to find `spec.selector`, `spec.podSelector`, `spec.namespaceSelector`
    3. Try to find `spec.match`, `spec.rules` (common policy CRD patterns)
    4. Always produce at least one Constraint with ConstraintType=Unknown
    5. Summary fallback: "{Kind} {name} in {namespace} (discovered automatically)"

### TASK-1.7: Wire discovery engine to indexer via adapters
- file: `internal/discovery/engine.go`
- test: `internal/discovery/engine_test.go` (pre-written)
- verify: `go test ./internal/discovery/ -v -count=1`
- spec: |
    Complete the TODO in `engine.go` scan(). When a new GVR is discovered:
    1. Create a dynamic informer via `dynamicinformer.NewFilteredDynamicSharedInformerFactory`
    2. Register event handlers on the informer:
       - AddFunc: look up adapter via `registry.ForGVR(gvr)`, or use generic adapter.
         Call `adapter.Parse(obj)`. For each returned Constraint, call `indexer.Upsert(c)`.
       - UpdateFunc: same as Add (Upsert handles replacement).
       - DeleteFunc: call `indexer.Delete(obj.GetUID())`.
    3. Start the informer in a goroutine.
    4. Handle parse errors: log + increment metric, do NOT crash.
    Engine now takes an `*indexer.Indexer` in its constructor.

### TASK-1.8: Implement correlation engine
- file: `internal/correlator/correlator.go`
- test: `internal/correlator/correlator_test.go` (pre-written)
- contract: `internal/correlator/doc.go`
- verify: `go test ./internal/correlator/ -v -count=1`
- spec: |
    Watches K8s Events (all namespaces, type=Warning). For each event:
    1. Extract involvedObject (namespace, name, kind, labels — may need to GET the object)
    2. Query `indexer.ByNamespace(ns)` then filter by label match
    3. For each matching constraint, emit a `CorrelatedNotification{Event, Constraint, Workload}`
    4. Send to notification dispatcher via a channel
    Rate limit: max 100 events processed per second (token bucket).
    Deduplicate: track (eventUID, constraintUID) pairs seen in last 5 minutes.

### TASK-1.9: Implement notification dispatcher
- file: `internal/notifier/dispatcher.go`
- test: `internal/notifier/dispatcher_test.go` (pre-written)
- contract: `internal/notifier/doc.go`
- verify: `go test ./internal/notifier/ -v -count=1`
- spec: |
    Receives `CorrelatedNotification` from correlator. For each:
    1. Determine detail level from NotificationPolicy (default: summary for developers)
    2. Render message at that detail level (see docs/PRIVACY_MODEL.md):
       - summary: constraint type + generic effect + remediation contact
       - detailed: + specific ports + constraint name (same namespace only)
       - full: + cross-namespace details + policy source
    3. Create a K8s Event on the affected workload (developer-scoped message)
    4. Update the ConstraintReport for the namespace
    Deduplication: suppress re-notification for same (constraintUID, workloadUID)
    within configurable window (default 60 min).
    Rate limit: max 100 events/minute per namespace (circuit breaker).

### TASK-1.10: Wire everything in main.go
- file: `cmd/controller/main.go`
- verify: `go build ./cmd/controller/ && echo "OK"`
- spec: |
    Wire all subsystems together:
    1. Create adapter registry, register all Phase 1 adapters
    2. Create indexer
    3. Create discovery engine with registry + indexer
    4. Create correlator with indexer
    5. Create dispatcher
    6. Connect correlator output channel → dispatcher input
    7. Start all subsystems with manager context
    8. Add discovery engine, correlator, dispatcher as Runnables to manager

### TASK-1.11: Integration test
- file: `test/integration/suite_test.go`, `test/integration/discovery_test.go`
- verify: `go test ./test/integration/ -v -count=1 -tags=integration`
- spec: |
    Use envtest. Install CRDs. Create a NetworkPolicy.
    Verify: discovery engine finds it → adapter parses it → indexer stores it
    → ConstraintReport appears in the namespace with correct entry.
    Create a ResourceQuota at 85% usage.
    Verify: ConstraintReport shows Warning severity for that quota.

---

## Phase 1.5: Agent-Consumable Outputs

These tasks make Nightjar's runtime outputs consumable by AI agents, kubectl plugins, and automation. They can run in parallel with Phase 2+.

### TASK-1.5.1: Implement structured Event annotations
- file: `internal/notifier/event_builder.go`, `internal/notifier/event_builder_test.go`
- verify: `go test ./internal/notifier/ -v -count=1 -run TestEventBuilder`
- spec: |
    When creating a Kubernetes Event, populate ALL annotation and label keys
    from `internal/annotations/keys.go`. The `EventStructuredData` annotation
    is a JSON-serialized constraint result. Privacy scoping applies: redact
    cross-namespace details at summary level.
    Build an EventBuilder: `func BuildEvent(c types.Constraint, level types.DetailLevel, workload ObjectReference) *corev1.Event`

### TASK-1.5.2: Implement workload annotation updater
- file: `internal/notifier/workload_annotator.go`, `internal/notifier/workload_annotator_test.go`
- verify: `go test ./internal/notifier/ -v -count=1 -run TestWorkloadAnnotator`
- spec: |
    Watches indexer changes. For each affected workload, PATCH its annotations:
    `nightjar.io/status`, `/constraints` (JSON), `/max-severity`,
    `/critical-count`, `/warning-count`, `/info-count`, `/last-evaluated`.
    Debounce: max 1 PATCH per workload per 30s. Remove annotations when zero constraints.

### TASK-1.5.3: Populate MachineReadable section in ConstraintReport
- file: `internal/notifier/report_reconciler.go`
- verify: `go test ./internal/notifier/ -v -count=1 -run TestReportReconciler_MachineReadable`
- spec: |
    When updating ConstraintReport, populate `status.machineReadable` with
    MachineConstraintEntry objects including sourceRef, affectedWorkloads with
    matchReason, structured remediation, metrics for ResourceLimit, and tags.
    Populate missingResources[] from requirement evaluator. Set schemaVersion="1".

### TASK-1.5.4: Implement remediation builder
- file: `internal/notifier/remediation.go`, `internal/notifier/remediation_test.go`
- verify: `go test ./internal/notifier/ -v -count=1 -run TestRemediation`
- spec: |
    Converts types.Constraint → structured RemediationInfo with typed steps.
    Per adapter: NetworkPolicy → kubectl get + contact platform. ResourceQuota →
    kubectl describe + request increase. Webhook → kubectl get + docs link.
    MissingResource → YAML template. Each step has requiresPrivilege.
    Shared by: ConstraintReport, MCP server, Events, kubectl plugin.

### TASK-1.5.5: Implement MCP server
- file: `internal/mcp/server.go`, `internal/mcp/handlers.go`, `internal/mcp/server_test.go`
- verify: `go test ./internal/mcp/ -v -count=1`
- spec: |
    MCP server per `internal/mcp/doc.go`. Tools: query, explain, check,
    list_namespaces, remediation. Resources: reports/{ns}, constraints/{ns}/{name},
    health, capabilities. The explain tool fuzzy-matches error messages to
    constraint summaries. The check tool parses YAML manifests and queries indexer.
    SSE transport on configurable port. Privacy via PrivacyResolverFunc.

### TASK-1.5.6: Implement kubectl plugin
- file: `cmd/kubectl-sentinel/main.go`, `cmd/kubectl-sentinel/query.go`
- verify: `go build ./cmd/kubectl-sentinel/`
- spec: |
    CLI: query, explain, check, remediate, status commands.
    -o json output matches MCP response schemas exactly (shared Go types).
    -o table for humans. Reads ConstraintReport CRDs + Event annotations.
    Does NOT require MCP server running.

### TASK-1.5.7: Implement /api/v1/capabilities endpoint
- file: `internal/api/capabilities.go`, `internal/api/capabilities_test.go`
- verify: `go test ./internal/api/ -v -count=1`
- spec: |
    HTTP endpoint returning JSON: enabled adapters, watched resource counts,
    hubble/MCP status, total indexed constraints, namespace count, last scan time.
    Agents hit this first to discover what's available.

---

## Phase 2: Cilium + Hubble

- [ ] **TASK-2.1**: Implement Cilium NetworkPolicy adapter
- [ ] **TASK-2.2**: Implement CiliumClusterwideNetworkPolicy adapter
- [ ] **TASK-2.3**: Implement Hubble flow client
- [ ] **TASK-2.4**: Implement flow-to-constraint correlation
- [ ] **TASK-2.5**: Build service map from Service/Endpoint objects
- [ ] **TASK-2.6**: Integration test with Cilium in Kind

## Phase 3: Gatekeeper + Kyverno + Webhook

- [ ] **TASK-3.1**: Implement Gatekeeper constraint adapter
- [ ] **TASK-3.2**: Implement Kyverno policy adapter
- [ ] **TASK-3.3**: Implement admission webhook (warning mode)
- [ ] **TASK-3.4**: Webhook certificate management
- [ ] **TASK-3.5**: Integration test with Gatekeeper
- [ ] **TASK-3.6**: Integration test with Kyverno
