# Tasks: Provider Tool Use and Action Intent Emission via Native MCP

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~1,200–1,600 total (PR 1: ~200, PR 2: ~520–650, PR 3: ~560–720) |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR 1 (port contract) → PR 2 (MCP server package) → PR 3 (adapters + cmd wiring) |
| Delivery strategy | auto-chain |
| Chain strategy | feature-branch-chain |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain
400-line budget risk: High

### Branch Topology (feature-branch-chain, maintainer-selected)

Tracker branch `feat/provider-tool-use-action-intents` accumulates the full integration and is the
only branch that merges to `main`. Child PRs never target `main`.

| PR | Branch | Targets | Tasks |
|----|--------|---------|-------|
| PR 1 | `feat/mcp-port-capabilities` | `feat/provider-tool-use-action-intents` (tracker) | Phase 1 (1.1–1.8) |
| PR 2 | `feat/mcp-tool-server` | `feat/mcp-port-capabilities` (PR 1 branch) | Phase 2 (2.1–2.9) |
| PR 3 | `feat/mcp-adapter-wiring` | `feat/mcp-tool-server` (PR 2 branch) | Phase 3 (3.1–3.10) + Phase 4 |
| Tracker | `feat/provider-tool-use-action-intents` | `main` | final integration only |

Each child PR's review diff stays scoped to its own slice because it is diffed against the previous
slice's branch, not against `main`.

> **Size exception notes**: PR 1 is under the 400-line budget. PR 2 and PR 3 each exceed 400 authored lines as single cohesive work units that cannot be split further while keeping `go test ./...` green at every cut. Maintainer `size:exception` is expected for PR 2 and PR 3 before their apply.

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | `ProviderCapabilities` type + `Capabilities()` on all three `Provider` implementers + supervisor budget override | PR 1 | `go test ./core/port/... ./core/supervisor/... ./adapters/claudecode/... ./adapters/opencode/...` | N/A — no binary change; stub `Capabilities()` returns zero value; supervisor falls back to zero budget (no cap), preserving existing behavior | Revert `core/port/provider.go`, `core/port/fake/fake_provider.go`, `core/supervisor/supervisor.go`, stub methods in both adapter files |
| 2 | `transport/mcp/` package — registry, sink, tools, HTTP server — tested in isolation with go-sdk client | PR 2 | `go test -race ./transport/mcp/...` | N/A — `transport/mcp` is intentionally unreferenced from `cmd/company` after this PR; `httptest.NewServer` exercises the full HTTP boundary without a real binary | Delete `transport/mcp/` directory entirely; revert `go.mod` and `go.sum` additions |
| 3 | Adapter MCP wiring (mint/drain + ephemeral config + text-only parsing) + `cmd/company` server lifecycle | PR 3 | `go test -race ./adapters/claudecode/... ./adapters/opencode/... ./cmd/company/...` | `go build ./cmd/company` — binary must start MCP server before agents; fake-binary integration tests exercise the full mint→tools/call→drain path | Revert all adapter changes, testdata fake binary changes, and `cmd/company/wire.go` / `cmd/company/main.go` wiring additions |

---

## Phase 1: Port Capabilities Contract (PR 1 — Work Unit 1)

All Phase 1 tasks belong to PR 1. Adding `Capabilities()` to `port.Provider` breaks compilation of all
three implementers simultaneously; all four Go source files **must land in the same PR**. The `var _ port.Provider = (*Provider)(nil)` compile-time guards in `fake_provider.go`, `adapters/claudecode/adapter.go`, and `adapters/opencode/adapter.go` catch any missing method immediately.

Spec coverage: `provider-adapter` delta spec — all four scenarios (supervisor reads capabilities,
operator override, provider default, fake.Provider conforms).

- [x] 1.1 [RED] Write failing capability contract tests in `core/port/contract_test.go`

Add to `core/port/contract_test.go` (package `port_test`):

- `TestProvider_Capabilities_ReturnsConfigured` — configure `fake.Provider` with
  `ReturnCapabilities: port.ProviderCapabilities{ContextBudget: 8000, ActionKinds: []string{"telegram_send"}}`,
  call `p.Capabilities()`, assert exact match on both fields
  (spec: provider-adapter – "fake.Provider conforms to the capability contract").
- `TestProvider_Capabilities_ZeroByDefault` — fresh `&fake.Provider{}`, call `Capabilities()`,
  assert zero `ProviderCapabilities{}` (zero value is valid; zero `ContextBudget` means no cap).

Expected failure: **compile error** — `port.ProviderCapabilities` undefined;
`fake.Provider` has no field `ReturnCapabilities`; method `Capabilities()` undefined.

Command: `go test ./core/port/...`

- [x] 1.2 [RED] Write failing supervisor budget-override tests in `core/supervisor/supervisor_test.go`

Add to `core/supervisor/supervisor_test.go` (package `supervisor` — internal, can call unexported helpers):

- `TestEffectiveBudget_OverrideTakesPrecedence` — build a `Supervisor` with
  `Config{ContextBudget: 200, Provider: &fake.Provider{ReturnCapabilities: port.ProviderCapabilities{ContextBudget: 8000}}}`;
  call `s.effectiveBudget()`, assert result is `200`
  (spec: provider-adapter – "An operator-configured override takes precedence").
- `TestEffectiveBudget_FallsBackToProvider` — same setup but `Config.ContextBudget = 0`,
  assert `effectiveBudget()` returns `8000`
  (spec: provider-adapter – "The provider-declared budget is the default absent an override").
- `TestEffectiveBudget_BothZero` — both zero, assert result is `0` (no cap).

Expected failure: **compile error** — `port.ProviderCapabilities` undefined; `(*Supervisor).effectiveBudget` undefined.

Command: `go test ./core/supervisor/...`

- [x] 1.3 [GREEN] Add `ProviderCapabilities` and `Capabilities()` to `core/port/provider.go`

Modify `core/port/provider.go`:

- Add `ProviderCapabilities` struct immediately after `ActionIntent`:
  ```go
  type ProviderCapabilities struct {
      // ContextBudget is the maximum number of characters for assembled BoundedContext.
      // Zero means no cap.
      ContextBudget int
      // ActionKinds lists the action kinds this provider can emit as MCP tool calls.
      ActionKinds []string
  }
  ```
- Add `Capabilities() ProviderCapabilities` to the `Provider` interface, after `RunTask`.

After this change `go test ./core/port/...` fails: `fake.Provider` is missing `Capabilities()`
(caught by `var _ port.Provider = (*Provider)(nil)` at line 69 of `fake_provider.go`).

- [x] 1.4 [GREEN] Implement `Capabilities()` on `fake.Provider` in `core/port/fake/fake_provider.go`

Modify `core/port/fake/fake_provider.go`:

- Add `ReturnCapabilities port.ProviderCapabilities` field to the `Provider` struct
  (alongside the existing `ReturnRunResult` field).
- Add `Capabilities()` method — thread-safe under `f.mu`, returns `f.ReturnCapabilities`.
- Update `Reset()` to zero `ReturnCapabilities`.

The `var _ port.Provider = (*Provider)(nil)` check at line 69 must remain unchanged and must pass.

Command: `go test ./core/port/...` — capability tests now green.

- [x] 1.5 [GREEN] Add `Capabilities()` stub to `adapters/claudecode/adapter.go`

Modify `adapters/claudecode/adapter.go`:

- Add `Capabilities() port.ProviderCapabilities` method on `*Adapter` returning `port.ProviderCapabilities{}`.
  This is a zero stub for Slice 1; it will be replaced with a real implementation in Phase 3.
- Verify `var _ port.Provider = (*Adapter)(nil)` (already at bottom of file) still compiles cleanly.

Command: `go test ./adapters/claudecode/...`

- [x] 1.6 [GREEN] Add `Capabilities()` stub to `adapters/opencode/adapter.go`

Modify `adapters/opencode/adapter.go`:

- Add `Capabilities() port.ProviderCapabilities` method on `*Adapter` returning `port.ProviderCapabilities{}`.
  Same zero stub pattern as claudecode.
- Verify `var _ port.Provider = (*Adapter)(nil)` (already at bottom of file) compiles cleanly.

Command: `go test ./adapters/opencode/...`

- [x] 1.7 [GREEN] Add `effectiveBudget()` to `core/supervisor/supervisor.go` and wire into context assembly

Modify `core/supervisor/supervisor.go`:

- Add private `effectiveBudget() int` method on `*Supervisor`:
  returns `s.cfg.ContextBudget` when non-zero; otherwise returns `s.cfg.Provider.Capabilities().ContextBudget`.
- In `executeDelegation` (line 411 in current source), replace
  `assembleBoundedContext(history, s.cfg.ContextBudget)` with
  `assembleBoundedContext(history, s.effectiveBudget())`.

After this change, `TestEffectiveBudget_*` tests must be green.

Command: `go test ./core/supervisor/...`

- [x] 1.8 [VERIFY] Full Slice 1 green check

Run:
```
go test ./core/port/... ./core/supervisor/... ./adapters/claudecode/... ./adapters/opencode/...
go build ./...
```

All pre-existing tests plus the new capability and budget tests must pass. The build must succeed
with the new interface method present on all four `port.Provider` implementers.

---

## Phase 2: MCP Server, Registry, Sink, and Tools (PR 2 — Work Unit 2)

All Phase 2 tasks belong to PR 2. Branch `feat/mcp-tool-server`, branched from and targeting
`feat/mcp-port-capabilities` (the PR 1 branch) — not `main`. The entire `transport/mcp/`
directory is new. It is intentionally **unreferenced** from `cmd/company` or any adapter after
this PR — `go build ./...` green, package isolated.

Spec coverage: all eight scenarios in `mcp-tool-server/spec.md`.
Threat-matrix coverage: Network exposure (bind failure), Unbounded sink (cap enforced).

> **Size note**: Phase 2 is estimated at 520–650 authored lines. Single cohesive package; cannot
> split further while keeping tests green. `size:exception` expected from maintainer before apply.

- [x] 2.1 Add `github.com/modelcontextprotocol/go-sdk v1.8.0` to `go.mod`

Modify `go.mod`:

- Add `github.com/modelcontextprotocol/go-sdk v1.8.0` to the `require` block (design Decision A).
- Run `go mod tidy` to compute and append all transitive checksums to `go.sum`.
- Run `go mod verify` — must pass before any other Phase 2 task.

Note: `go.sum` additions are automated checksums, not authored code. They are included in snapshot
identity but excluded from the authored-lines budget calculation.

- [x] 2.2 [RED] Write failing unit tests for `Registry` in `transport/mcp/registry_test.go`

Create `transport/mcp/registry_test.go` (package `mcp` or `mcp_test`):

- `TestRegistry_MintResolveAndDrain` — Mint token for `{tenant: "acme", agent: "worker", taskID: "t1"}`;
  Resolve → assert identity matches; Drain the handle → returns empty slice initially;
  Resolve again → assert error (entry deleted)
  (spec: mcp-tool-server – "Token resolves to the invoking tenant, agent, and task";
  "Unknown or expired token is rejected").
- `TestRegistry_UnknownTokenRejected` — call Resolve with a token never minted, assert non-nil error.
  (spec: mcp-tool-server – "Unknown or expired token is rejected").
- `TestRegistry_DrainedTokenBehavesAsUnknown` — Mint, Drain, Resolve → error;
  assert idempotency: second Drain on handle returns empty slice without panic.
- `TestRegistry_ForeignTenantRejected` — Mint for tenant `"acme"`, server is configured for tenant
  `"beta"`; Resolve must return error; zero intents accessible
  (spec: mcp-tool-server – "A token resolving to a different tenant is rejected").
- `TestRegistry_ParallelMintAndDrain` — 50 goroutines each Mint, append one intent via handle's
  sink, then Drain; assert all drains return exactly one intent and no races
  (design: "Concurrency — parallel agents make this load-bearing; -race covers it").

Expected failure: **compile error** — `transport/mcp` package does not exist.

Command: `go test -race ./transport/mcp/...`

- [x] 2.3 [RED] Write failing unit tests for `Sink` in `transport/mcp/sink_test.go`

Create `transport/mcp/sink_test.go`:

- `TestSink_RecordAndRead` — create Sink with cap=10; record 3 intents;
  Read returns all 3 in insertion order with correct Kind/Payload.
- `TestSink_CapEnforced` — create Sink with cap=2; record 2 intents (success);
  record 3rd intent → assert non-nil error returned, third intent NOT in Read result
  (design: Threat matrix – "Unbounded sink — Per-invocation intent cap; excess rejected";
  planned RED test: "Cap enforced").
- `TestSink_EmptyRead` — fresh Sink, Read → empty slice (not nil, no error).

Expected failure: **compile error** — `transport/mcp` package does not exist.

Command: `go test ./transport/mcp/...`

- [x] 2.4 [RED] Write failing integration tests for `Server` in `transport/mcp/server_test.go`

Create `transport/mcp/server_test.go`. Tests use `httptest.NewServer` and the go-sdk MCP client.
A spy `fakeGateway` struct is defined locally to assert zero gateway calls.

- `TestServer_ToolRegistryMirrorsPolicy` — start Server with `policies = {"telegram_send": {}}`;
  list tools via SDK client; assert exactly one tool named `"telegram_send"` and no tool for any
  undeclared kind (spec: mcp-tool-server – "Tool registry mirrors configured action kinds").
- `TestServer_ToolDescription_DiscloseContract` — list tools; assert each tool's description
  contains the words "records intent" and "does not" (or equivalent disclosure per design Decision C:
  "records intent; does not perform the action; no result observable this turn; call once")
  (spec: mcp-tool-server – "Tool description discloses the acknowledge-without-execute contract").
- `TestServer_ValidToolCall_RecordsIntentAndNoGatewaySend` — Mint token for tenant `"acme"`,
  send `tools/call` for `"telegram_send"` with `Authorization: Bearer <token>`;
  assert response is success (`isError: false`), assert intent with Kind `"telegram_send"` recorded
  in sink for that token; assert spy gateway call count is zero
  (spec: mcp-tool-server – "A valid tool call is acknowledged without side effects";
  design: "Integration — tools/call never reaches Gateway.Send — Spy gateway asserts zero calls").
- `TestServer_SinkIsAuthoritative_CLITextIgnored` — Mint token; call `tools/call` once;
  drain sink; assert `ActionIntents` reflects exactly one intent from the sink regardless of what
  any hypothetical CLI stdout would contain
  (spec: mcp-tool-server – "Sink is authoritative regardless of CLI text output").
- `TestServer_UnknownTokenRejected_ZeroIntents` — send `tools/call` with a token that was never
  minted; assert request rejected (non-success response); assert zero intents accessible
  (spec: mcp-tool-server – "Unknown or expired token is rejected").
- `TestServer_ForeignTenantRejected_ZeroIntents` — Mint token for `"acme"`, present it in a
  context where the registry's tenant check expects `"beta"`; assert rejection, zero intents
  (spec: mcp-tool-server – "A token resolving to a different tenant is rejected").
- `TestServer_Dedupe_RepeatCallReturnsSameReceipt` — Mint token; call `tools/call` twice with
  identical tool + identical payload; assert second response contains same receipt ID and
  `duplicate: true`; assert only one intent recorded in sink
  (design: Decision C – "Dedupe: key token + kind + canonical(payload). A repeat returns the
  same receipt with duplicate:true and records nothing").
- `TestServer_BindFailure_ReturnsError` — attempt to start Server on an address already occupied
  by a listener; assert `Start()` returns a non-nil error immediately
  (design: Threat matrix – "Bind failure aborts materialize before any supervisor is ready").

Expected failure: **compile error** — `transport/mcp` package does not exist.

Command: `go test -race ./transport/mcp/...`

- [x] 2.5 [GREEN] Create `transport/mcp/registry.go`

Create `transport/mcp/registry.go` (package `mcp`):

- `invocation` struct: `tenant, agent, taskID string`; `sink *Sink`; `dedupe map[string]string`
  (maps dedupeKey → receipt ID); `mu sync.Mutex` for dedupe map.
- `Handle` struct: pointer to owning `invocation`; pointer to `Registry` (for deletion on Drain).
- `Registry` struct: `mu sync.RWMutex`; `entries map[string]*invocation`.
- `func NewRegistry() *Registry`.
- `func (r *Registry) Mint(tenant, agent, taskID string, exp time.Time) (token string, handle *Handle)`:
  generate 32-byte `crypto/rand` random bytes encoded as base64url (design: "32 bytes crypto/rand,
  base64url. Entropy makes map lookup safe"); store entry; derive `TokenInfo.Expiration = exp`
  for SDK middleware.
- `func (r *Registry) Resolve(token, claimedTenant string) (*invocation, error)`: RLock;
  lookup; check tenant match; check expiry; return error for unknown, expired, or foreign-tenant token.
- `func (h *Handle) Drain() []port.ActionIntent`: Lock registry, delete entry, return sink contents;
  idempotent (second Drain returns empty slice).

- [x] 2.6 [GREEN] Create `transport/mcp/sink.go`

Create `transport/mcp/sink.go` (package `mcp`):

- `const defaultIntentCap = 100`.
- `Sink` struct: `mu sync.Mutex`; `intents []port.ActionIntent`; `cap int`.
- `func NewSink(cap int) *Sink` — uses `defaultIntentCap` when `cap <= 0`.
- `func (s *Sink) Record(intent port.ActionIntent) error` — returns a sentinel error when
  `len(intents) >= cap`; otherwise appends.
- `func (s *Sink) Read() []port.ActionIntent` — returns a defensive copy.

- [x] 2.7 [GREEN] Create `transport/mcp/tools.go`

Create `transport/mcp/tools.go` (package `mcp`):

- `func buildToolDescription(kind string) string` — returns the spec-mandated disclosure text
  (design Decision C): `"Calling this tool records an intent for <kind> for later policy
  classification and does not execute the action. Call once; the outcome is unavailable this turn."`.
- `func dedupeKey(token, kind string, payload map[string]any) string` — deterministic key:
  concatenate token + kind + sorted-key JSON encoding of payload (use `encoding/json` with
  `json.Marshal` on a `map[string]any` sorted by key via `sort.Strings`; stable serialization).
- `func buildAckResult(receipt, kind string, duplicate bool) *mcp.CallToolResult` (or
  equivalent SDK type) — `isError: false`; text content per design Decision C:
  `"Recorded intent <kind> as <receipt>. Pending classification; outcome unavailable this turn. Do not call again. Report as requested, not completed."`;
  structured content `{receipt, kind, status: "recorded", duplicate: <bool>}`.
- `func registerTools(srv *mcp.Server, policies map[string]config.Policy, registry *Registry)` —
  for each key in policies, call `srv.AddTool(...)` with handler that (1) resolves token from
  `req.Extra.TokenInfo`, (2) calls `registry.Resolve(token, claimedTenant)`, (3) checks dedupe,
  (4) records intent in sink, (5) returns `buildAckResult`.

- [x] 2.8 [GREEN] Create `transport/mcp/server.go`

Create `transport/mcp/server.go` (package `mcp`) using `github.com/modelcontextprotocol/go-sdk`:

- `Server` struct: `httpSrv *http.Server`; `listener net.Listener`; `registry *Registry`.
- `func New(policies map[string]config.Policy, registry *Registry) *Server` — creates an MCP
  `mcp.NewServer(...)`; calls `registerTools`; configures `auth.RequireBearerToken` middleware
  to validate bearer tokens (TTL enforced by middleware; no hand-rolled expiry check)
  (design: "TokenInfo.Expiration and UserID are middleware-enforced, so TTL and session-pinning
  are not hand-rolled"); wraps with `mcp.NewStreamableHTTPHandler`.
- `func (s *Server) Start(addr string) error` — `net.Listen("tcp", addr)`; if error, return
  immediately (design: "Bind failure aborts materialize before any supervisor is ready";
  Threat matrix planned RED test: "Bind failure aborts, no adapter invoked").
  On success: `s.listener = ln`; `s.httpSrv = &http.Server{Handler: handler}`; start goroutine
  `s.httpSrv.Serve(ln)`.
- `func (s *Server) Addr() string` — returns `s.listener.Addr().String()` (loopback + ephemeral port).
- `func (s *Server) Shutdown(ctx context.Context) error`.
- Bind address is always loopback (`127.0.0.1:0` for tests; callers may pass `127.0.0.1:0` in
  production too for ephemeral port assignment) (design: Threat matrix – "Bind 127.0.0.1:0").
- Note: `auth.RequireBearerToken` is the SDK middleware; token content is resolved via
  `req.Extra.TokenInfo`; the handler passes `TokenInfo.UserID` (which holds the opaque token)
  to `registry.Resolve` (design: "mirrors the tenantInterceptor pattern in
  transport/a2a/server.go:44,61").

- [x] 2.9 [REFACTOR] Verify Phase 2

Run:
```
go test -race ./transport/mcp/...
go build ./...
```

All eight server integration tests, five registry unit tests, and three sink unit tests must pass.
`go build ./...` must succeed with `transport/mcp` unreferenced from `cmd/company`.

---

## Phase 3: Adapter MCP Integration and cmd Wiring (PR 3 — Work Unit 3)

All Phase 3 tasks belong to PR 3. Branch `feat/mcp-adapter-wiring`, branched from and targeting
`feat/mcp-tool-server` (the PR 2 branch) — not `main`. This PR closes the loop:
adapters call Mint/Drain, CLIs receive ephemeral MCP config, and `cmd/company` starts the server
before any supervisor is marked ready.

Spec coverage: all scenarios in `provider-action-intent-emission/spec.md`.
Threat-matrix coverage: Subprocess argv (token absent), Ephemeral config (mtime unchanged).

> **Size note**: Phase 3 is estimated at 560–720 authored lines. `size:exception` expected from
> maintainer before apply.

- [x] 3.1 [RED] Write failing MCP-aware tests for claudecode adapter in `adapters/claudecode/adapter_test.go`

Add to `adapters/claudecode/adapter_test.go` (package `claudecode_test`):

- `TestClaudeAdapter_MCPToolCall_PopulatesActionIntents` — start an in-process `transport/mcp.Server`
  with policy `{"telegram_send": config.Policy{}}`, mint a token, configure `Adapter` with the
  server URL and registry, run the fakeclaude binary (which will be extended in task 3.4 to call
  `tools/call` when env var `FAKECLAUDE_CALL_MCP=1` is set), assert
  `result.ActionIntents` has exactly one entry `{Kind: "telegram_send"}`
  (spec: provider-action-intent-emission – "A real tool call yields a non-empty ActionIntents";
  "Claude fake-binary test proves the real path is wired").
- `TestClaudeAdapter_NoToolCall_EmptyIntents` — same setup but `FAKECLAUDE_CALL_MCP` not set;
  assert `result.ActionIntents` is empty and `err` is nil
  (spec: provider-action-intent-emission – "No tool call yields an empty, not erroneous, result").

Expected failure: compile error — `Adapter` has no MCP-related fields; `Options` does not accept
registry or server URL.

Command: `go test ./adapters/claudecode/...`

- [x] 3.2 [RED] Write failing threat-matrix tests for claudecode adapter in `adapters/claudecode/adapter_test.go`

Add:

- `TestClaudeAdapter_TokenAbsentFromArgv` — run `RunTask` with MCP wiring; inside fakeclaude binary
  introspect its own argv (the binary prints its argv to a temp file); assert the bearer token
  string does not appear in any argv element
  (design: Threat matrix – "Token in an HTTP header, never argv/ps; argv stays a slice";
  planned RED test: "Token absent from argv").
- `TestClaudeAdapter_NoJsonSchema_InArgv` — run `RunTask`, assert `"--json-schema"` is absent
  from the subprocess argv (spec: provider-action-intent-emission –
  "Claude adapter does not request `--json-schema`").
- `TestClaudeAdapter_EphemeralConfig_TempFileRemoved` — run `RunTask`, assert that no MCP config
  temp file under `t.TempDir()` exists after the subprocess exits; assert Claude's persistent
  config path (if writable) mtime is unchanged
  (spec: provider-action-intent-emission – "Claude adapter configures MCP ephemerally";
  design: Threat matrix – "Temp file 0600 + --strict-mcp-config; persisted mtime unchanged").

Expected failure: compile error.

Command: `go test ./adapters/claudecode/...`

- [x] 3.3 [RED] Write failing MCP-aware tests for opencode adapter in `adapters/opencode/adapter_test.go`

Add to `adapters/opencode/adapter_test.go` (package `opencode_test`):

- `TestOpenCodeAdapter_MCPToolCall_PopulatesActionIntents` — start in-process server, mint token,
  configure `Adapter`, run fakeopencode with `FAKEOPENCODE_CALL_MCP=1`; assert non-empty
  `ActionIntents` with `Kind: "telegram_send"`
  (spec: provider-action-intent-emission – "OpenCode fake-binary test proves the real path is wired").
- `TestOpenCodeAdapter_NoToolCall_EmptyIntents` — `FAKEOPENCODE_CALL_MCP` not set; assert empty
  `ActionIntents`, nil error.
- `TestOpenCodeAdapter_EphemeralConfig_PersistedMtimeUnchanged` — run `RunTask`; assert no
  modification to `~/.opencode/config.json` mtime (or confirm it does not exist and is not created)
  (spec: provider-action-intent-emission – "OpenCode adapter configures MCP ephemerally";
  design: Threat matrix – "`OPENCODE_CONFIG_CONTENT` scoped to that process env").
- `TestOpenCodeAdapter_TerminatesOnEOF_NoHang` — fakeopencode outputs one NDJSON line then exits;
  assert `RunTask` returns within 1 second without blocking on any sentinel event
  (spec: provider-action-intent-emission – "OpenCode adapter terminates reader on process exit,
  not a sentinel event").

Expected failure: compile error.

Command: `go test ./adapters/opencode/...`

- [x] 3.4 [GREEN] Extend `adapters/claudecode/testdata/fakeclaude/main.go` to call MCP when instructed

Modify `adapters/claudecode/testdata/fakeclaude/main.go`:

- Preserve all existing behavior (safe:1 prefix, model flag, system-prompt flag, hang mode, fail mode, etc.).
- When env var `FAKECLAUDE_CALL_MCP=1` is set:
  1. Parse `FAKECLAUDE_MCP_CONFIG` env var containing the path to the ephemeral MCP config JSON file
     (same file the adapter writes for `--mcp-config`).
  2. Read and parse the JSON: extract the first server URL and `Authorization: Bearer <token>` header.
  3. Use go-sdk MCP client to POST `tools/call` for `"telegram_send"` with
     payload `{"body": "test-intent"}` to that URL with the Authorization header.
  4. Continue normal output after the MCP call (whether it succeeds or fails for argv/output tests).
- When env var `FAKECLAUDE_DUMP_ARGV=1` is set: write `os.Args` as newline-separated strings
  to the path given in `FAKECLAUDE_ARGV_FILE` before producing normal output (for token-absent-from-argv test).

- [x] 3.5 [GREEN] Extend `adapters/opencode/testdata/fakeopencode/main.go` to call MCP when instructed

Modify `adapters/opencode/testdata/fakeopencode/main.go`:

- Same pattern as fakeclaude.
- When `FAKEOPENCODE_CALL_MCP=1`:
  1. Parse `OPENCODE_CONFIG_CONTENT` env var (which the adapter sets); extract server URL and token
     from the JSON config (same env var the real opencode CLI would read).
  2. Use go-sdk MCP client to call `tools/call` for `"telegram_send"` with `{"body": "test-intent"}`.
  3. Emit one NDJSON text event then exit (mimicking the opencode `--format json` stream format for
     text-only extraction by the adapter).
- When `FAKEOPENCODE_DUMP_ARGV=1`: write argv to `FAKEOPENCODE_ARGV_FILE`.

- [x] 3.6 [GREEN] Modify `adapters/claudecode/adapter.go` — add MCP mint/drain and ephemeral config

Modify `adapters/claudecode/adapter.go`:

- Define a local `tokenMinter` interface (structural typing; avoids importing `transport/mcp`):
  ```go
  type tokenMinter interface {
      Mint(tenant, agent, taskID string, exp time.Time) (token string, handle drainer)
  }
  type drainer interface {
      Drain() []port.ActionIntent
  }
  ```
- Add fields to `Options`:
  - `MCPRegistry tokenMinter` — set by `cmd/company`; nil for existing tests without MCP wiring.
  - `MCPServerAddr string` — loopback address of the running MCP server.
  - `Tenant string` — tenant for token minting.
  - `PolicyActionKinds []string` — for `Capabilities()` return value.
  - `ContextBudget int` — for `Capabilities()` return value.
- Add corresponding fields to `Adapter` struct.
- Update `New()` to capture these from `Options`.
- Update `RunTask()`:
  1. If `mcpRegistry != nil`: call `registry.Mint(tenant, agentName, taskID, deadline+30s)` →
     `token`, `handle`.
  2. Write ephemeral MCP config JSON to `os.CreateTemp("", "mcp-config-*.json")` with mode 0600;
     JSON format: `{"mcpServers": {"framework": {"type": "http", "url": "http://<addr>",
     "headers": {"Authorization": "Bearer <token>"}}}}`.
     Defer `os.Remove(tempFile)` to guarantee cleanup.
  3. Add `--mcp-config <tempfile>`, `--strict-mcp-config` to argv.
  4. Add `--output-format`, `stream-json`, `--verbose` to argv for NDJSON stream parsing.
  5. Set `FAKECLAUDE_MCP_CONFIG=<tempfile>` on subprocess env (for test doubles only; real claude
     reads the config via `--mcp-config` flag; test doubles read the same file via env var for
     simpler test wiring).
  6. Switch output parsing: read NDJSON from stdout; for each line with `type: "text"` or
     `type: "assistant"`, accumulate text content into `ProviderResult.Output`. Do NOT parse
     any `tool_use` events (design: "Stream parsing extracts text only").
  7. After `cmd.Wait()`: if `handle != nil`, call `handle.Drain()` →
     `result.ActionIntents`.
  8. Verify token string does not appear anywhere in argv slice (assert at coding time).
- Replace zero-stub `Capabilities()` with real implementation: return
  `port.ProviderCapabilities{ContextBudget: a.contextBudget, ActionKinds: a.policyActionKinds}`.

- [x] 3.7 [GREEN] Modify `adapters/opencode/adapter.go` — add MCP mint/drain and ephemeral config

Modify `adapters/opencode/adapter.go`:

- Same `tokenMinter` / `drainer` local interface pattern as claudecode (copy verbatim to keep adapters decoupled).
- Add same fields to `Options` and `Adapter`.
- Update `RunTask()`:
  1. Mint token if `mcpRegistry != nil`.
  2. Build `OPENCODE_CONFIG_CONTENT` JSON with MCP server entry and bearer token; set it in
     subprocess `Env` as `append(os.Environ(), "OPENCODE_CONFIG_CONTENT="+configJSON)` (subprocess-scoped;
     do NOT call `os.Setenv`) (design: Threat matrix – "`OPENCODE_CONFIG_CONTENT` scoped to
     that process env").
  3. Add `--format json` to argv for NDJSON output.
  4. Switch output parsing: read NDJSON line-by-line; extract text from relevant event fields;
     stop on EOF or process exit — **not** on any dedicated terminal event line
     (spec: provider-action-intent-emission – "OpenCode adapter terminates reader on process exit").
  5. After `cmd.Wait()`: if `handle != nil`, call `handle.Drain()` → `result.ActionIntents`.
- Replace zero-stub `Capabilities()` with real implementation.

- [x] 3.8 [GREEN] Modify `cmd/company/wire.go` — start MCP server before agents

Modify `cmd/company/wire.go`:

- Add `mcpServer *mcp.Server` field to `wireOptions` (for test injection).
- At the start of `materializeAgents`, before the agent loop:
  1. If `opts.mcpServer == nil`: call `mcp.New(cfg.RiskPolicy, mcp.NewRegistry())`; call
     `server.Start("127.0.0.1:0")`; if error, return it immediately — no supervisor is
     started (spec: mcp-tool-server – "Server startup failure aborts materialize";
     design: "bind failure aborts materialize before any supervisor is ready").
  2. Store server reference in local variable (and in `opts.mcpServer` so shutdown can reach it).
- When constructing each adapter inside the loop, pass
  `MCPRegistry: server.Registry()`, `MCPServerAddr: server.Addr()`,
  `Tenant: cfg.Tenant`, `PolicyActionKinds: policyKindKeys(cfg.RiskPolicy)`,
  `ContextBudget: agCfg.ContextBudget` (if that field exists; or keep zero for default).
- Add `func policyKindKeys(policies map[string]config.Policy) []string` helper.
- The returned `[]*agentRuntime` should include the server reference or the caller receives it
  via a new return value / via `opts.mcpServer` for shutdown.

- [x] 3.9 [GREEN] Update `cmd/company/main.go` — add MCP server shutdown to lifecycle

Modify `cmd/company/main.go` in `runMaterialize`:

- After `materializeAgents` returns, extract the started MCP server (via return value or a new
  `wireOptions` field that `materializeAgents` populates).
- Add `defer mcpSrv.Shutdown(ctx)` to the shutdown sequence alongside `uiSrv.Shutdown`.
- The MCP server startup log line: `fmt.Fprintf(opts.stderr, "mcp: server listening on %s\n", srv.Addr())`.

- [x] 3.10 [VERIFY] Full Slice 3 verification

Run:
```
go test -race ./adapters/claudecode/... ./adapters/opencode/... ./cmd/company/...
go build ./cmd/company
go test -race ./...
go vet ./...
```

All new MCP-integration tests plus all pre-existing tests must pass with `-race`. The binary must
build without errors.

---

## Phase 4: Cleanup and Documentation

- [x] 4.1 Document opencode `--format json` version floor

Modify `AGENTS.md` (or a new `docs/adapter-compatibility.md` if preferred) to add:

> The opencode adapter requires `--format json` NDJSON output support. Verified with
> `opencode >= 1.18.31` (local verification; no upstream confirmation available as of this change).

This closes design Open Questions item: "opencode --format json version floor unverified upstream —
document '1.18.31 verified'".

- [x] 4.2 Final full regression check

Run:
```
go test -race ./...
go vet ./...
go build ./cmd/company
```

All tests across the full module must be green with the race detector enabled.

---

## Spec–Task Traceability

| Spec / Requirement | Tasks |
|---|---|
| mcp-tool-server: Server Starts Once at Framework Startup | 2.8, 3.8, 3.9, 2.4 (BindFailure test) |
| mcp-tool-server: One Tool Per Risk-Policy Action Kind | 2.7, 2.4 (ToolRegistryMirrorsPolicy) |
| mcp-tool-server: Per-Invocation Token Binds Adapter | 2.5, 2.2, 2.4, 3.6, 3.7 |
| mcp-tool-server: tools/call Acknowledges Without Executing | 2.7, 2.8, 2.4 (ValidToolCall), 3.2 |
| mcp-tool-server: Intent Sink Is the Sole Source of ActionIntents | 2.6, 2.4 (SinkAuthoritative) |
| provider-action-intent-emission: Ephemeral Per-Invocation MCP Config | 3.6, 3.7, 3.2, 3.3 |
| provider-action-intent-emission: ActionIntents From Sink, Never Stream Parsing | 3.6, 3.7, 3.1, 3.3 |
| provider-action-intent-emission: Structured Output Parsing Extracts Text Only | 3.6, 3.7, 3.2 (NoJsonSchema) |
| provider-action-intent-emission: Adapter-Level Tests Exercise Real Adapter Path | 3.1, 3.3, 3.4, 3.5 |
| provider-adapter: Providers Declare Their Capabilities | 1.3, 1.4, 1.5, 1.6 |
| provider-adapter: Supervisor Reads Capabilities Before Context Assembly | 1.7, 1.2 |
| provider-adapter: Operator Override Takes Precedence | 1.7, 1.2 |
| provider-adapter: Provider-Declared Budget Is the Default | 1.7, 1.2 |
| provider-adapter: fake.Provider Conforms | 1.4, 1.1 |

## Threat-Matrix Coverage

| Boundary | Planned RED test | Tasks |
|---|---|---|
| Subprocess argv | `TestClaudeAdapter_TokenAbsentFromArgv` | 3.2 |
| Ephemeral config (claude) | `TestClaudeAdapter_EphemeralConfig_TempFileRemoved` | 3.2 |
| Ephemeral config (opencode) | `TestOpenCodeAdapter_EphemeralConfig_PersistedMtimeUnchanged` | 3.3 |
| Network exposure (bind failure) | `TestServer_BindFailure_ReturnsError` | 2.4 |
| Unbounded sink | `TestSink_CapEnforced` | 2.3 |
