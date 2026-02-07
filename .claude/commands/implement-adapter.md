Implement the adapter for the policy engine specified by the user. Follow this exact sequence:

1. Read `internal/adapters/networkpolicy/adapter.go` as the reference implementation
2. Read the `doc.go` contract in the target adapter's package (e.g., `internal/adapters/<name>/doc.go`)
3. Read all test fixture YAML files in the target adapter's `testdata/` directory — pay attention to `# EXPECT:` comments
4. Read the pre-written test file (`adapter_test.go`) to understand exact expected outputs
5. Implement `adapter.go` following the exact pattern from the networkpolicy reference
6. Register the adapter in `internal/adapters/registry.go` if not already registered
7. Run `go test ./internal/adapters/<name>/ -v -count=1` and iterate until all tests pass
8. Run `go build ./...` to confirm nothing is broken
