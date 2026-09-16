# Apply Progress: provider-tool-use-action-intents

**Change**: provider-tool-use-action-intents
**Mode**: Strict TDD
**Store**: hybrid (Engram + openspec file)
**Delivery**: feature-branch-chain, auto-chain
**Branch**: feat/mcp-adapter-wiring → targets feat/mcp-tool-server (current, Phase 3)

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

## Phase 3: Adapter MCP Integration and cmd Wiring (PR 3 — Work Unit 3) — COMPLETE

### Completed Tasks

- [x] 3.1 [RED] Write failing MCP-aware tests for claudecode adapter in `adapters/claudecode/adapter_test.go`
- [x] 3.2 [RED] Write failing threat-matrix tests for claudecode adapter in `adapters/claudecode/adapter_test.go`
- [x] 3.3 [RED] Write failing MCP-aware tests for opencode adapter in `adapters/opencode/adapter_test.go`
- [x] 3.4 [GREEN] Extend `adapters/claudecode/testdata/fakeclaude/main.go` to call MCP when instructed
- [x] 3.5 [GREEN] Extend `adapters/opencode/testdata/fakeopencode/main.go` to call MCP when instructed
- [x] 3.6 [GREEN] Modify `adapters/claudecode/adapter.go` — add MCP mint/drain and ephemeral config
- [x] 3.7 [GREEN] Modify `adapters/opencode/adapter.go` — add MCP mint/drain and ephemeral config
- [x] 3.8 [GREEN] Modify `cmd/company/wire.go` — start MCP server before agents
- [x] 3.9 [GREEN] Update `cmd/company/main.go` — add MCP server shutdown to lifecycle
- [x] 3.10 [VERIFY] Full Slice 3 verification

### Phase 3 TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 3.1 | `adapters/claudecode/adapter_test.go` | Adapter (real subprocess + real in-process MCP server) | ✅ 17/17 pre-existing claudecode tests passing before edit | ✅ compile error: `claudecode.Drainer`/`Options.MCPRegistry` undefined | ✅ 2 tests pass after 3.4+3.6 (MCPToolCall / NoToolCall) | ✅ 2 cases (tool call vs no call) | ➖ None needed |
| 3.2 | `adapters/claudecode/adapter_test.go` | Adapter (threat-matrix) | same baseline as 3.1 | ✅ compile error (same undefined symbols) | ✅ 3 tests pass after 3.4+3.6 (TokenAbsentFromArgv / NoJsonSchema / EphemeralConfigRemoved) | ✅ 3 cases | ✅ Added raw-text fallback in `parseStreamText` after discovering real `claude --version` short-circuits to plain (non-NDJSON) output — see Deviations |
| 3.3 | `adapters/opencode/adapter_test.go` | Adapter (real subprocess + real in-process MCP server) | ✅ 13/13 pre-existing opencode tests passing before edit | ✅ compile error: `opencode.Drainer`/`Options.MCPRegistry` undefined | ✅ 4 tests pass after 3.5+3.7 (MCPToolCall / NoToolCall / PersistedMtimeUnchanged / TerminatesOnEOF) | ✅ 4 cases | ➖ None needed |
| 3.4 | — | — | N/A (testdata fake binary) | N/A | ✅ 5 claudecode MCP/threat tests pass using the extended fake | — | ➖ None needed |
| 3.5 | — | — | N/A (testdata fake binary) | N/A | ✅ 4 opencode MCP/threat tests pass using the extended fake | — | ➖ None needed |
| 3.6 | — | — | ✅ (3.1/3.2 baseline) | N/A | ✅ 17 pre-existing + 5 new claudecode tests pass | — | ✅ Extracted `parseStreamText`/`writeEphemeralMCPConfig` helpers; raw-text fallback added |
| 3.7 | — | — | ✅ (3.3 baseline) | N/A | ✅ 13 pre-existing + 4 new opencode tests pass | — | ✅ Extracted `parseStreamText`/`buildMCPConfigJSON` helpers |
| 3.8 | `cmd/company/cmd_test.go`, `cmd/company/e2e_test.go` (pre-existing, re-run as safety net) | Integration | ✅ all pre-existing cmd/company tests passing before edit | N/A (wiring change, no new test file) | ✅ `go build ./cmd/company` + all pre-existing cmd/company tests still pass with real MCP server started in `materializeAgents` | N/A | ➖ None needed |
| 3.9 | same as 3.8 | Integration | same as 3.8 | N/A | ✅ same suite green with shutdown wired | N/A | ➖ None needed |
| 3.10 | — | Verify | — | — | ✅ `go build ./...`, `go vet ./...`, `gofmt -l .` clean, `go test -race ./...` all green | — | — |

### Phase 3 Files Changed

| File | Action | What Was Done |
|------|--------|---------------|
| `adapters/claudecode/adapter.go` | Modified | Added `Drainer`/`TokenMinter` exported interfaces, MCP `Options` fields, mint/drain wiring in `RunTask`, ephemeral `--mcp-config` temp-file writer (0600, removed via `defer`), unconditional `--output-format stream-json --verbose`, NDJSON text-extraction parser with raw-text fallback, real `Capabilities()` |
| `adapters/claudecode/adapter_test.go` | Modified | Added `registryMinter`/`spyMinter` test wrappers, `startTestMCPServer` helper, and 5 new tests: `TestClaudeAdapter_MCPToolCall_PopulatesActionIntents`, `TestClaudeAdapter_NoToolCall_EmptyIntents`, `TestClaudeAdapter_TokenAbsentFromArgv`, `TestClaudeAdapter_NoJsonSchema_InArgv`, `TestClaudeAdapter_EphemeralConfig_TempFileRemoved` |
| `adapters/claudecode/testdata/fakeclaude/main.go` | Modified | Added `FAKECLAUDE_DUMP_ARGV`/`FAKECLAUDE_ARGV_FILE`, `FAKECLAUDE_DUMP_MCP_CONFIG_PATH`, `FAKECLAUDE_CALL_MCP` (real go-sdk MCP client call to `telegram_send`); default-case output now emitted as one NDJSON `{"type":"text","text":"..."}` line; new flags (`--mcp-config`, `--strict-mcp-config`, `--output-format`, `--verbose`) consumed without disturbing input parsing |
| `adapters/opencode/adapter.go` | Modified | Same pattern as claudecode: `Drainer`/`TokenMinter`, MCP `Options` fields, mint/drain wiring, `OPENCODE_CONFIG_CONTENT` built via `buildMCPConfigJSON` and set subprocess-scoped only (never `os.Setenv`), unconditional `--format json`, NDJSON text-extraction parser with raw-text fallback, real `Capabilities()` |
| `adapters/opencode/adapter_test.go` | Modified | Added `registryMinter` test wrapper, `startTestMCPServer` helper, and 4 new tests: `TestOpenCodeAdapter_MCPToolCall_PopulatesActionIntents`, `TestOpenCodeAdapter_NoToolCall_EmptyIntents`, `TestOpenCodeAdapter_EphemeralConfig_PersistedMtimeUnchanged`, `TestOpenCodeAdapter_TerminatesOnEOF_NoHang` |
| `adapters/opencode/testdata/fakeopencode/main.go` | Modified | Added `FAKEOPENCODE_CALL_MCP` (real go-sdk MCP client call to `telegram_send`); default-case output now emitted as one NDJSON `{"type":"text","text":"..."}` line; `--format` flag consumed without disturbing input parsing |
| `cmd/company/wire.go` | Modified | Added `claudeRegistryMinter`/`opencodeRegistryMinter` wrapper types adapting `*transmcp.Registry` to each adapter's exported `TokenMinter`; `policyKindKeys` helper; starts the MCP server (real `transmcp.New(cfg.Tenant, cfg.RiskPolicy, transmcp.NewRegistry())` + `Start("127.0.0.1:0")`) before the agent loop, aborting `materializeAgents` on bind failure; wires `MCPRegistry`/`MCPServerAddr`/`Tenant`/`AgentName`/`PolicyActionKinds` into both adapter constructors; added `agentRuntime.mcpSrv` and `wireOptions.mcpServer` (test injection) |
| `cmd/company/main.go` | Modified | Shuts down the shared MCP server (`runtimes[0].mcpSrv`) alongside the A2A servers in `runMaterialize`'s shutdown sequence |
| `openspec/changes/.../tasks.md` | Modified | Marked tasks 3.1–3.10 complete |

### Phase 3 Deviations from Design/Tasks

1. **Task 3.8 — `mcp.New` signature.** `tasks.md` proposed `mcp.New(cfg.RiskPolicy, mcp.NewRegistry())`. The real Phase 2 signature (`transport/mcp/server.go:31`) is `func New(tenant string, policies map[string]config.Policy, registry *Registry) *Server`. Implemented against the real signature: `transmcp.New(cfg.Tenant, cfg.RiskPolicy, transmcp.NewRegistry())`. The `tenant` argument is load-bearing — tool handlers call `registry.Resolve(token, tenant)` to satisfy the `ForeignTenantRejected` scenario.

2. **Tasks 3.6/3.7 — `tokenMinter`/`drainer` made exported (`TokenMinter`/`Drainer`), not unexported as originally sketched.** `tasks.md` proposed:
   ```go
   type tokenMinter interface { Mint(tenant, agent, taskID string, exp time.Time) (token string, handle drainer) }
   ```
   The real shipped method is `func (r *Registry) Mint(tenant, agent, taskID string, exp time.Time) (string, *Handle)` (`transport/mcp/registry.go:57`). Go has no covariant return types: `*mcp.Registry` cannot satisfy an interface whose method returns `drainer` by returning `*mcp.Handle`, even though `*mcp.Handle` structurally has a matching `Drain()` method — interface satisfaction requires the implementing method's declared return type to be identical to the interface's declared type, not merely assignable to it.

   Resolution chosen: `cmd/company/wire.go` supplies a tiny wrapper type per adapter (`claudeRegistryMinter`, `opencodeRegistryMinter`) that adapts `*transmcp.Registry` to the adapter-local `TokenMinter` interface — exactly as the injected deviation note suggested. The wrapper's `Mint` method literally declares `(string, claudecode.Drainer)` (or `opencode.Drainer`) as its return type; internally it returns the wrapper's own concrete value, which Go implicitly converts to the interface type at the `return` statement. This is only possible because `TokenMinter`/`Drainer` are **exported** in each adapter package — an unexported type cannot be named (and therefore cannot be spelled as a return type) outside its declaring package, so `wire.go` could not have declared a method returning an unexported `claudecode.drainer`. Exporting these two interfaces still keeps `adapters/claudecode` and `adapters/opencode` free of any `transport/mcp` import, preserving the design's decoupling intent; only `cmd/company/wire.go` (the composition root) imports `transport/mcp`.

3. **Unconditional `--output-format`/`--format json` broke a pre-existing real-CLI integration test.** Switching output parsing to NDJSON text-extraction (per spec: "Structured Output Parsing Extracts Text Only") is applied unconditionally, matching `tasks.md`'s literal ordering of RunTask steps. This required updating both fake binaries to always wrap their default-case output as one `{"type":"text","text":"..."}` NDJSON line, which was verified against the **real** `claude --output-format stream-json --verbose` wire format (`{"type":"assistant","message":{"content":[{"type":"text","text":"..."}]}}`) captured live from the installed `claude` CLI — `parseStreamText` handles both the bare `{"type":"text",...}` shape (used by the fakes) and the real `assistant`/`message.content[]` shape. One pre-existing test, `TestContract_RunTask_RealClaude`, broke transiently: it passes the literal string `"--version"` as the task input, and the real `claude` CLI intercepts `--version` anywhere in argv as a special case that prints a **plain, non-JSON** version string regardless of `--output-format`. Fixed by adding a raw-text fallback in both adapters: if NDJSON extraction yields an empty string but the raw buffer is non-empty, the raw trimmed text is used instead — verified against the real `claude` binary (`go test -race ./adapters/claudecode/... -count=1` green, including the real-CLI test, which is only skipped in `-short` mode or when `claude` is not on `PATH`).

### Phase 3 Verification

- `go build ./...`: clean
- `go vet ./...`: clean
- `gofmt -l .`: nothing (clean)
- `go test -race ./adapters/claudecode/... ./adapters/opencode/... ./cmd/company/... -count=1`: all green (claudecode 22/22 incl. real-CLI test, opencode 17/17, cmd/company all pre-existing tests green)
- `go build ./cmd/company`: clean, produces a working binary
- `go test -race ./... -count=1`: all 12 packages green

### Phase 3 Work Unit Evidence

| Evidence | Value |
|---|---|
| Focused test command | `go test -race ./adapters/claudecode/... ./adapters/opencode/... ./cmd/company/... -count=1` → all green |
| Runtime harness | `go build ./cmd/company` — binary builds and starts the MCP server before the agent loop; fake-binary integration tests (`TestClaudeAdapter_MCPToolCall_PopulatesActionIntents`, `TestOpenCodeAdapter_MCPToolCall_PopulatesActionIntents`) exercise the full mint → real HTTP `tools/call` → drain path end-to-end against a real `transport/mcp.Server` |
| Rollback boundary | Revert `adapters/claudecode/adapter.go`, `adapters/claudecode/adapter_test.go`, `adapters/claudecode/testdata/fakeclaude/main.go`, `adapters/opencode/adapter.go`, `adapters/opencode/adapter_test.go`, `adapters/opencode/testdata/fakeopencode/main.go`, `cmd/company/wire.go`, `cmd/company/main.go` — Phase 1 and Phase 2 code is untouched by this slice |

### Phase 3 PR Boundary

- Mode: chained PR slice (feature-branch-chain, slice 3 of 3); `size:exception` expected (forecast 560–720 authored lines)
- Branch: `feat/mcp-adapter-wiring`, targets `feat/mcp-tool-server` (never `main`)
- Not committed by this apply batch — delivery (commit/push/PR) is an explicit human decision per the session's git boundary instructions

---

## Remaining Tasks

- [ ] Phase 4 (4.1–4.2): Documentation + final regression
