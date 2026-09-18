# Apply Progress: provider-tool-use-action-intents

**Change**: provider-tool-use-action-intents
**Mode**: Strict TDD
**Store**: hybrid (Engram + openspec file)
**Delivery**: feature-branch-chain, auto-chain
**Branch**: feat/mcp-tool-server → targets feat/mcp-port-capabilities

---

## Phase 1: Port Capabilities Contract (PR 1 — Work Unit 1) — COMPLETE

### Completed Tasks

- [x] 1.1 [RED] Write failing capability contract tests in `core/port/contract_test.go`
- [x] 1.2 [RED] Write failing supervisor budget-override tests in `core/supervisor/supervisor_test.go`
- [x] 1.3 [GREEN] Add `ProviderCapabilities` and `Capabilities()` to `core/port/provider.go`
- [x] 1.4 [GREEN] Implement `Capabilities()` on `fake.Provider` in `core/port/fake/fake_provider.go`
- [x] 1.5 [GREEN] Add `Capabilities()` stub to `adapters/claudecode/adapter.go`
- [x] 1.6 [GREEN] Add `Capabilities()` stub to `adapters/opencode/adapter.go`
- [x] 1.7 [GREEN] Add `effectiveBudget()` to `core/supervisor/supervisor.go` and wire into context assembly
- [x] 1.8 [VERIFY] Full Slice 1 green check

### Phase 1 Commits
- abef6df (implementation)
- ac6b26f (tasks.md)

---

## Phase 2: MCP Server, Registry, Sink, and Tools (PR 2 — Work Unit 2) — COMPLETE

### Completed Tasks

- [x] 2.1 Add `github.com/modelcontextprotocol/go-sdk v1.8.0` to `go.mod`
- [x] 2.2 [RED] Write failing unit tests for `Registry` in `transport/mcp/registry_test.go`
- [x] 2.3 [RED] Write failing unit tests for `Sink` in `transport/mcp/sink_test.go`
- [x] 2.4 [RED] Write failing integration tests for `Server` in `transport/mcp/server_test.go`
- [x] 2.5 [GREEN] Create `transport/mcp/registry.go`
- [x] 2.6 [GREEN] Create `transport/mcp/sink.go`
- [x] 2.7 [GREEN] Create `transport/mcp/tools.go`
- [x] 2.8 [GREEN] Create `transport/mcp/server.go`
- [x] 2.9 [REFACTOR] Verify Phase 2

### Phase 2 TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 2.1 | — | — | N/A | N/A | ✅ go mod verify passes | N/A | N/A |
| 2.2 | `transport/mcp/registry_test.go` | Unit | N/A (new pkg) | ✅ compile error: `NewRegistry undefined` | ✅ 6 registry tests pass after 2.5 | ✅ 6 cases | ➖ None needed |
| 2.3 | `transport/mcp/sink_test.go` | Unit | N/A (new pkg) | ✅ compile error: `NewSink undefined` | ✅ 3 sink tests pass after 2.6 | ✅ 3 cases | ➖ None needed |
| 2.4 | `transport/mcp/server_test.go` | Integration | N/A (new pkg) | ✅ compile error: package does not exist | ✅ 8 server tests pass after 2.5–2.8 | ✅ 8 scenarios per spec | ✅ UnknownTokenRejected fixed for 401-at-Connect |
| 2.5 | — | — | N/A | N/A | ✅ 6 registry tests pass | — | ➖ None needed |
| 2.6 | — | — | N/A | N/A | ✅ 3 sink tests pass | — | ➖ None needed |
| 2.7 | — | — | N/A | N/A | ✅ server tests pass | — | ➖ None needed |
| 2.8 | — | — | N/A | N/A | ✅ all 17 tests pass | — | ➖ None needed |
| 2.9 | — | Verify | — | — | ✅ ALL 12 packages ok with -race | — | — |

### Phase 2 Files Changed

| File | Action | What Was Done |
|------|--------|---------------|
| `go.mod` | Modified | Added `github.com/modelcontextprotocol/go-sdk v1.8.0` direct dependency |
| `go.sum` | Modified | Transitive checksums for go-sdk and deps |
| `transport/mcp/sink.go` | Created | Bounded intent buffer, Record, Read (defensive copy) |
| `transport/mcp/registry.go` | Created | Mint/Resolve/Drain, TokenVerifier() for auth middleware |
| `transport/mcp/tools.go` | Created | buildToolDescription, dedupeKey, buildAckResult, registerTools |
| `transport/mcp/server.go` | Created | HTTP MCP server with bearer-token auth middleware |
| `transport/mcp/sink_test.go` | Created | 3 sink unit tests |
| `transport/mcp/registry_test.go` | Created | 6 registry unit tests incl. 50-goroutine parallel test |
| `transport/mcp/server_test.go` | Created | 8 server integration tests |
| `openspec/changes/.../tasks.md` | Modified | Marked tasks 2.1–2.9 complete |

### Phase 2 Deviations from Design

- `New(tenant, policies, registry)` has a `tenant` parameter not listed in the design's signature. Required for `registry.Resolve(token, tenant)` in tool handlers (ForeignTenantRejected spec requirement).
- `TestServer_UnknownTokenRejected`: SDK rejects unknown tokens at `initialize` (401 HTTP). Test adapted to accept rejection at Connect or CallTool layer.

### Phase 2 Verification

- `go build ./...`: clean
- `go vet ./...`: clean
- `gofmt -l .`: nothing
- `go test ./transport/mcp/... -count=1 -race`: 17/17 PASS
- `go test ./... -count=1 -race`: ALL 12 packages OK

### Phase 2 Work Unit Evidence

| Evidence | Value |
|---|---|
| Focused test command | `go test -race ./transport/mcp/... -count=1` → 17 tests pass |
| Runtime harness | N/A — package intentionally unreferenced from cmd/company; `Server.Start("127.0.0.1:0")` exercises full HTTP boundary |
| Rollback boundary | Delete `transport/mcp/` directory; revert `go.mod` and `go.sum` additions |

### Phase 2 Commits

- c95ba26 (implementation: transport/mcp package)
- 1daa348 (tasks.md: Phase 2 marked complete)

### PR Boundary

- Mode: chained PR slice (feature-branch-chain, slice 2 of 3); `size:exception` expected
- Branch: `feat/mcp-tool-server`, targets `feat/mcp-port-capabilities`
- Authored lines: ~540 (single cohesive package; cannot split and keep `go test` green)

---

## Remaining Tasks

- [ ] Phase 3 (3.1–3.10): Adapter MCP wiring + cmd/company lifecycle (PR 3)
- [ ] Phase 4 (4.1–4.2): Documentation + final regression
