# Tasks: Agent Startup Framework — Foundation

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~2,400–2,800 (8 slices) |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR 1 → PR 2 → PR 3 → PR 4 → PR 5 → PR 6 → PR 7 → PR 8 |
| Delivery strategy | auto-chain |
| Chain strategy | stacked-to-main |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: stacked-to-main
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | Go toolchain + module + `address` + `config` loader | PR 1 | `go test ./core/address/... ./config/...` | N/A — no supervisor yet; pure unit logic | Delete `go.mod`, `core/address/`, `config/` |
| 2 | `core/port` interfaces + `Provider` contract tests with fake | PR 2 | `go test ./core/port/...` | N/A — no supervisor yet; contract exercised via fake only | Delete `core/port/`; PR 1 stays intact |
| 3 | `transport/a2a` wiring + supervisor state machine on real loopback | PR 3 | `go test ./transport/a2a/... ./core/supervisor/...` | `go run ./cmd/company/ materialize` starts two supervisors | Delete `transport/a2a/`, `core/supervisor/`; PRs 1–2 stay intact |
| 4 | `core/policy` engine + approval-token minting | PR 4 | `go test ./core/policy/...` | N/A — policy is pure classification; no subprocess needed | Delete `core/policy/`; PRs 1–3 stay intact |
| 5 | `adapters/claudecode` — Claude Code adapter | PR 5 | `go test ./adapters/claudecode/...` | `claude --version` (adapter spawns real process in non-`-short` tests) | Delete `adapters/claudecode/`; PRs 1–4 stay intact |
| 6 | `gateways/telegram` port adapter + contract test + fake | PR 6 | `go test ./gateways/telegram/...` | N/A for live Telegram (env-gated integration only) | Delete `gateways/telegram/`; PRs 1–5 stay intact |
| 7 | `ui/` embedded monitoring UI (HTML/CSS/JS + `//go:embed`) | PR 7 | `go test ./ui/...` (embed compiles) | `go run ./cmd/company/ materialize` + open browser | Delete `ui/`; PRs 1–6 stay intact |
| 8 | `cmd/company` composition root + E2E synthetic loop | PR 8 | `go test -run TestE2E ./...` | Full two-supervisor loop: hard-deny + reject + approve | Revert `cmd/company/main.go` only; all packages stay |

---

## PR 1 — Module Bootstrap, `address`, `config` (~220–280 lines)

**Goal**: Buildable Go module with toolchain, `A2AAddress` keying, and `company.yaml` loader.
**Satisfies**: `company-as-code` REQ: company definition schema, inline-secret rejection; `a2a-transport` REQ: tenant keying foundation.
**Dependencies**: none.
**DoD**: `go build ./...` passes; `go test ./core/address/... ./config/...` green.
**Rollback**: delete `go.mod`, `go.sum`, `core/address/`, `config/`, `cmd/company/` stub.

- [x] 1.1 Install Go ≥1.25.0 on the development machine (prerequisite — blocks all compile tasks). **DONE** — `go1.27.0` at `/usr/local/go`, verified compiling and running. **Caveat for automated runs**: `~/.zshrc` exports the PATH correctly for interactive shells, but non-interactive shells do not read `.zshrc`. Any tooling or sub-agent must prepend `export PATH=$PATH:/usr/local/go/bin` or invoke `/usr/local/go/bin/go` directly.
- [x] 1.2 Create `go.mod` (`module github.com/.../ai-solo-startup-framework`, `go 1.25`) and run `go get github.com/a2aproject/a2a-go/v2` to pin it; commit `go.sum`.
- [x] 1.3 Create `core/address/address.go`: define `A2AAddress` as `"{agent-name}/{tenant}"` string type; add `New(name, tenant string) (A2AAddress, error)` rejecting empty tenant; add `Parse(s string) (A2AAddress, error)`.
- [x] 1.4 Write `core/address/address_test.go`: table-driven tests — valid address roundtrips, empty-tenant rejected, missing-slash rejected, agent-name-only rejected.
- [x] 1.5 Create `config/schema.go`: define `CompanyConfig`, `AgentConfig`, `GatewayConfig`, `TelegramGatewayConfig`, `RiskPolicyEntry`, `PolicyConfig` structs matching the `company.yaml` schema; add `Load(path string) (*CompanyConfig, error)` that YAML-parses and validates.
- [x] 1.6 Add inline-secret guard in `config/schema.go`: `Load` must reject any `company.yaml` whose `gateways.telegram.token_env` value looks like a raw token (non-env-var string); fails with a descriptive error.
- [x] 1.7 Write `config/schema_test.go`: valid file accepted; inline token rejected; missing `token_env` field rejected; agent-level gateway field rejected or produces validation error (satisfies `company-as-code` scenario "Agent-level gateway field is rejected or ignored").
- [x] 1.8 Create empty `cmd/company/main.go` stub (`package main; func main() {}`) so `go build ./...` compiles from day 1.

---

## PR 2 — `core/port` Interfaces + Provider Contract Tests (~180–230 lines)

**Goal**: `Provider` and `Gateway` port interfaces with a `fakeProvider` that passes all contract tests.
**Satisfies**: `provider-adapter` ALL requirements; establishes the seam that makes PR 5 (Claude Code) safe.
**Dependencies**: PR 1 (`A2AAddress`, `a2a-go` types).
**DoD**: `go test ./core/port/...` green; `fakeProvider` passes every contract scenario.
**Rollback**: delete `core/port/`; `cmd/company` stub stays.

- [x] 2.1 Create `core/port/provider.go`: define `Provider` interface (`Complete`, `CompleteError`, `SendMessage`, `SendMessageStream`, `ResolveAgent`, `SendTask`), `TaskResult`, `TaskOptions`, `StreamEvent`, `BoundedContext`, `ResumePoint` types as per orchestrator spec; no adapter imports.
- [x] 2.2 Create `core/port/gateway.go`: define `Gateway` interface (`Send(ctx, msg OutboundMessage) error`); `OutboundMessage`, `Receipt` types; `ValidateChannel` enforces allow-list (telegram, email); no gateway-concrete imports.
- [x] 2.3 Create `core/port/fake/fake_provider.go`: `fake.Provider` struct implementing `port.Provider` — configurable return values; records calls for assertions; thread-safe.
- [x] 2.4 Write `core/port/contract_test.go`: contract test suite run against `fake.Provider` and `fake.Gateway`; covers all `provider-adapter` spec scenarios: full-address invocation, Complete/CompleteError idempotency, SendMessage wait/nowait, SendMessageStream, ResolveAgent known/unknown, SendTask with full-address preservation, Gateway allow-list enforcement, non-invocation assertion foundation.
- [x] 2.5 Write `core/port/fake/fake_gateway.go`: `fake.Gateway` implementing `port.Gateway`; validates channel allow-list; records `Send` calls; `CallCount`/`LastCall`/`WasCalled` helpers for negative assertions. Used by PR 6 contract tests and E2E tests.

---

## PR 3 — `transport/a2a` + Supervisor State Machine on Real Loopback (~350–400 lines)

**Goal**: A2A JSON-RPC server per agent, tenant interceptor, SSE push, supervisor lifecycle states, per-task `TaskState` machine, crash/restart recovery with JSON-file store.
**Satisfies**: `a2a-transport` ALL; `agent-supervisor` ALL.
**Dependencies**: PRs 1–2.
**DoD**: `go test ./transport/a2a/... ./core/supervisor/...` green; integration test (skippable with `-short`) starts two real loopback supervisors and calls `ListTasks`.
**Rollback**: delete `transport/a2a/`, `core/supervisor/`; ports and address packages stay.

- [x] 3.1 Create `transport/a2a/server.go`: wrap `a2asrv` handler; assign loopback port per supervisor; serve signed Agent Card at `/.well-known/agent.json`; wire `CallInterceptor` that rejects `req.Tenant == ""` before task processing (satisfies `a2a-transport` scenario "Empty tenant rejected").
- [x] 3.2 Write `transport/a2a/server_test.go` with integration test (`testing.Short()` skip): start real server, fetch Agent Card — assert discoverable (satisfies `a2a-transport` scenario "Newly started supervisor is discoverable"); send request with empty tenant — assert rejected.
- [x] 3.3 Create `core/supervisor/state.go`: define supervisor lifecycle FSM (`STARTING → IDLE ⇄ WORKING`, `IDLE → DRAINING → STOPPED`, `crash → RECOVERING → IDLE`); task `TaskState` transitions; state is not exported beyond the package — only observable via `Status()`.
- [x] 3.4 Create `core/supervisor/store.go`: JSON-file task store keyed by `A2AAddress`; implements load/save/update; resolves the design's open question — use JSON files per `ponytail` (stdlib, no KV needed for one-machine v1).
- [x] 3.5 Create `core/supervisor/supervisor.go`: `Supervisor` struct; wires `a2asrv`, task queue, `store`, `Provider` port (injected at construction); implements `AgentExecutor.Execute` iter; manages IDLE/WORKING transitions; on non-zero provider exit emits `FAILED` (satisfies `agent-supervisor` scenario "Provider failure marks task FAILED"); on start loads store and enters `RECOVERING` if open tasks found (satisfies crash/restart scenarios).
- [x] 3.6 Create `core/supervisor/context.go`: `assembleBoundedContext` — assembles `{task input, role-memory slice, prior resolutions}` capped by `ProviderCapabilities.ContextBudget`; drops oldest-first when over budget; prepends truncation marker to the context when truncated (satisfies `agent-supervisor` bounded-context scenarios).
- [x] 3.7 Write `core/supervisor/supervisor_test.go` (unit, table-driven): lifecycle transitions (STARTING→IDLE, IDLE→WORKING→IDLE); task state progression; FAILED on provider error; bounded-context truncation with marker (both within-budget and over-budget scenarios).
- [x] 3.8 Write `core/supervisor/store_test.go`: store round-trip keyed by full `A2AAddress`; assert loading with one tenant does not return records of another tenant (satisfies multi-tenancy isolation scenario from `company-as-code`).
- [x] 3.9 Write integration test `core/supervisor/integration_test.go` (`testing.Short()` skip): start CEO + worker supervisors on loopback via real `transport/a2a`; CEO sends task to worker via `a2aclient`; assert `ListTasks` returns task in correct state (satisfies `a2a-transport` "CEO delegates to worker over real wire").

---

## PR 4 — `core/policy` Engine + Approval-Token Minting (~200–260 lines)

**Goal**: Two-stage classification (capability then risk), token minting, `INPUT_REQUIRED` escalation routing, approval-payload versioning.
**Satisfies**: `risk-policy-engine` ALL; `approval-flow` ALL.
**Dependencies**: PRs 1–3 (`A2AAddress`, `TaskState`, `ActionIntent`).
**DoD**: `go test ./core/policy/...` green; all threat-matrix RED tests for gateway-effect boundary pass.
**Rollback**: delete `core/policy/`; supervisor runs but processes no action intents.

- [x] 4.1 **RED** — `core/policy/policy_test.go`: write failing tests FIRST — (a) disallowed role → `REJECTED`, `Send` never called, no escalation raised; (b) allowed risky role → task enters `INPUT_REQUIRED`, no send yet; (c) allowed non-risky → executes directly; (d) hard-deny is `REJECTED` not `FAILED` (terminal state distinct from a gateway failure). These are threat-matrix gateway-effect cases (b), (e) — required before production code.
- [x] 4.2 Create `core/policy/engine.go`: `Engine.Classify(intent ActionIntent, role string, policy PolicyConfig) ClassificationResult` — step 1: check `allowed_roles`; if absent → `HardDeny`; step 2: if `risky` → `Escalate`; else → `Permit`. Returns `ClassificationResult{Kind, ApprovalToken?}`. Mints an opaque token only on `Permit` or pending approval.
- [x] 4.3 Create `core/policy/token.go`: `ApprovalToken` — opaque value the gateway requires; minted only by `Engine` on permitted or approved actions; `Validate` rejects tokens not minted by this engine instance; no token minted for `HardDeny` or unapproved escalations.
- [x] 4.4 Create `core/policy/payload.go`: versioned approval-request payload schema (carried in `TaskStatus.Message`); `MarshalPayload` / `ValidatePayload(version, data)` — rejects unrecognized version (satisfies `approval-flow` scenario "Unrecognized payload version rejected").
- [x] 4.5 Wire `Engine` into `core/supervisor/supervisor.go` (imported as a parameter): after provider returns `ProviderResult.ActionIntents`, classify each; `HardDeny` → `REJECTED`; `Escalate` → yield `INPUT_REQUIRED` event with versioned payload; `Permit` → call `ExecuteAction` with minted token.
- [x] 4.6 Extend `core/supervisor/supervisor_test.go`: add escalation-cycle test — full `SUBMITTED → WORKING → INPUT_REQUIRED → WORKING → COMPLETED` state sequence (satisfies `agent-supervisor` "Full escalation cycle traversal"); add restart-preserves-`INPUT_REQUIRED` test (satisfies `approval-flow` "Escalated task still resumable after restart").
- [x] 4.7 Write `core/policy/resume_test.go`: resume-is-new-message test — simulate `INPUT_REQUIRED` task; deliver approval as new `SendMessage` with matching task ID; assert supervisor recognizes it as resume not new task (satisfies `approval-flow` "Approval resumes correct parked task").

---

## PR 5 — `adapters/claudecode` — Claude Code Adapter (~220–270 lines)

**Goal**: First concrete `Provider` implementation; passes the PR 2 contract test suite unchanged.
**Satisfies**: `claude-code-adapter` ALL.
**Dependencies**: PRs 1–4 (port interfaces, policy wired).
**DoD**: `go test ./adapters/claudecode/...` green; contract suite from PR 2 runs against this adapter and passes (no adapter-specific changes to the suite).
**Rollback**: delete `adapters/claudecode/`; framework still builds with `fakeProvider`.

- [x] 5.1 **RED** — `adapters/claudecode/adapter_test.go`: write failing tests FIRST for threat-matrix provider-subprocess cases — (a) argv-as-slice with shell metacharacters in input → metacharacters are literal, no injection; (b) hung child killed after deadline → `FAILED`; (c) oversized output truncated with marker before parse; (d) non-zero exit → failure outcome, not success. Required before production code per threat matrix.
- [x] 5.2 Create `adapters/claudecode/adapter.go`: `ClaudeCodeAdapter` implementing `Provider`; uses `os/exec.CommandContext` with argv slice (never `sh -c`); injects `BoundedContext` as stdin/flags; parses stdout → `[]a2a.Part`; maps non-zero exit → `error`; enforces `ctx` deadline (kills child on expiry).
- [x] 5.3 Add output size cap in `adapters/claudecode/adapter.go`: read output via `io.LimitReader`; if limit hit, truncate and prepend marker before parsing (threat-matrix case c).
- [x] 5.4 Write `adapters/claudecode/contract_test.go`: import and run the contract test suite from `core/port/contract_test.go` against `ClaudeCodeAdapter` (integration-tagged, `testing.Short()` skip for real process; unit-level cases run against a test double that simulates `claude` exit behavior).
- [x] 5.5 Write `adapters/claudecode/invocation_test.go` (unit, table-driven): two invocations for same agent use separate `exec.Cmd` instances (no shared state); non-zero exit returns failure; raw output never exposed through port (all results are parsed `Part` values).

---

## PR 6 — `gateways/telegram` Port Adapter + Contract Test + Fake (~180–230 lines)

**Goal**: `Gateway` port implementation via Telegram Bot API; `fakeGateway` (already in PR 2) proven against real adapter by contract test; env-gated live test.
**Satisfies**: `telegram-gateway` ALL.
**Dependencies**: PRs 1–2 (`Gateway` port, `fakeGateway`).
**DoD**: `go test ./gateways/telegram/...` green; contract test passes for both real adapter and fake; live test skipped without `TELEGRAM_BOT_TOKEN` env.
**Rollback**: delete `gateways/telegram/`; policy and supervisor still run; E2E loop uses `fakeGateway`.

- [x] 6.1 **RED** — `gateways/telegram/gateway_test.go`: write failing tests FIRST — (a) `Send` called without token → refuses (fails closed); (b) inline token in config rejected at load (already in PR 1; assert here from gateway perspective); (c) caller-supplied recipient in message has no effect on actual send target (owner config wins); (d) failed API call → returns `error`, never panics (threat-matrix gateway-effect cases a, c, d, f).
- [x] 6.2 Create `gateways/telegram/gateway.go`: `TelegramGateway` implementing `Gateway`; reads `TELEGRAM_BOT_TOKEN` and `TELEGRAM_OWNER_ID` from env at construction (not at call time); recipient resolved solely from owner config; `Send` makes HTTP call to Telegram Bot API; returns `error` on any API failure; no inbound path.
- [x] 6.3 Write `gateways/telegram/contract_test.go`: run `core/port` contract assertions against `TelegramGateway` using a test HTTP server stub (no real Telegram needed); also run against `fakeGateway` from PR 2; assert both implementations produce equivalent behavior for message shape, error-on-failure, receipt (satisfies `telegram-gateway` "Send failure maps to failed task").
- [x] 6.4 Write `gateways/telegram/integration_test.go` (env-gated, `testing.Short()` skip): live send to real Telegram when `TELEGRAM_BOT_TOKEN` and `TELEGRAM_OWNER_ID` are set; verifies real API path is exercised.

---

## PR 7 — `ui/` Embedded Monitoring UI (~200–260 lines)

**Goal**: Hand-written HTML/CSS/JS served via `//go:embed`; `EventSource` for SSE; approve/reject controls only on `INPUT_REQUIRED` tasks.
**Satisfies**: `monitoring-ui` ALL.
**Dependencies**: PR 3 (SSE push stream, `ListTasks`).
**DoD**: `go test ./ui/...` (embed compiles, handler unit tests pass); manual check: two-agent company visible, approve/reject only on pending escalations.
**Rollback**: delete `ui/`; supervisor still starts; no UI served.

- [x] 7.1 Create `ui/index.html` + `ui/style.css` + `ui/app.js`: state list showing all agents (supervisor state + task states); `EventSource` connecting to SSE endpoint; approve/reject buttons rendered only for tasks in `INPUT_REQUIRED`; no other write actions (satisfies `monitoring-ui` "No control to stop running agent").
- [x] 7.2 Create `ui/embed.go`: `//go:embed index.html style.css app.js` directive; export `FS embed.FS`.
- [x] 7.3 Create `ui/handler.go`: `UIHandler` serves `ui.FS` on the CEO supervisor's HTTP mux; exposes `/api/tasks` (proxies `ListTasks`), `/api/approve` and `/api/reject` endpoints that post verdict to the supervisor (only valid for `INPUT_REQUIRED` tasks).
- [x] 7.4 Write `ui/handler_test.go`: `UIHandler` returns 200 on `GET /`; `/api/approve` on a non-`INPUT_REQUIRED` task returns 409 (satisfies "completed task offers no approve/reject control"); `/api/approve` on `INPUT_REQUIRED` task returns 200.
- [x] 7.5 Write `ui/handler_test.go` SSE test: connect to SSE endpoint with `httptest`; inject a state-change event; assert client receives it without polling (satisfies `monitoring-ui` "Escalation appears live without manual refresh").

---

## PR 8 — `cmd/company` Composition Root + E2E Synthetic Loop (~300–380 lines)

**Goal**: Full CLI (`materialize`, `start`), two-supervisor wiring, E2E test covering all three outcomes + caller-recipient ignored.
**Satisfies**: `company-as-code` materialize CLI; all `agent-supervisor`, `approval-flow`, `risk-policy-engine`, `telegram-gateway`, `monitoring-ui` E2E scenarios; multi-tenancy isolation scenario.
**Dependencies**: PRs 1–7 (everything).
**DoD**: `go test -run TestE2E ./...` green (using `fakeGateway`, no live Telegram required); `go build ./cmd/company/` produces a working binary.
**Rollback**: revert `cmd/company/main.go` and `cmd/company/e2e_test.go`; all packages stay intact.

- [x] 8.1 Replace `cmd/company/main.go` stub: implement `materialize` command — parse `company.yaml` via `config.Load`, start one `Supervisor` goroutine per agent with injected `Provider` and `Gateway`, serve UI on CEO's mux.
- [x] 8.2 Implement `cmd/company/wire.go`: composition root — the ONLY file that imports `adapters/claudecode` and `gateways/telegram` and wires them to `core/port` interfaces; all other packages remain import-clean.
- [x] 8.3 Add multi-tenancy isolation test in `cmd/company/e2e_test.go`: materialize tenant `acme`, then materialize tenant `beta` alongside it; assert no task or state from `acme` appears in `beta`'s `ListTasks` response (satisfies `company-as-code` "Two tenants coexist").
- [x] 8.4 **E2E negative 1** — `cmd/company/e2e_test.go`: start two supervisors with `fakeGateway`; instruct worker (`engineer` role) to emit `telegram_send` intent; assert task reaches `REJECTED`, `fakeGateway.Send` call count is zero, no `INPUT_REQUIRED` state observed (satisfies threat-matrix gateway case e, risk-policy "Disallowed role produces rejection with no escalation").
- [x] 8.5 **E2E negative 2** — `cmd/company/e2e_test.go`: CEO (`ceo` role) emits `telegram_send`; policy escalates to `INPUT_REQUIRED`; human rejects via `UIHandler`; assert `fakeGateway.Send` call count is zero, task reaches `REJECTED` (satisfies `approval-flow` "Rejection prevents send entirely").
- [x] 8.6 **E2E positive** — `cmd/company/e2e_test.go`: CEO emits `telegram_send`; escalates to `INPUT_REQUIRED`; human approves via `UIHandler`; assert `fakeGateway.Send` called exactly once; task progresses `SUBMITTED → WORKING → INPUT_REQUIRED → WORKING → COMPLETED` (satisfies `approval-flow` "Approval sends message exactly once", `agent-supervisor` "Full escalation cycle traversal").
- [x] 8.7 **E2E caller-recipient** — `cmd/company/e2e_test.go`: CEO emits `telegram_send` intent carrying an explicit recipient field; policy approves; assert `fakeGateway` received send to the configured owner, not the intent-supplied recipient (satisfies `telegram-gateway` "Agent-supplied recipient has no effect", threat-matrix gateway case f).
- [x] 8.8 Write `cmd/company/cmd_test.go`: `Load` rejects inline token (delegates to PR 1 guard, integrated here); `materialize` of a two-agent YAML starts two goroutines each discoverable (satisfies `company-as-code` "Materializing two-agent company starts two supervisors").

---

## Threat-Matrix Task Index

| Boundary | Case | Task |
|---|---|---|
| Provider subprocess | (a) argv-slice / no injection | 5.1 RED → 5.2 |
| Provider subprocess | (b) hung child killed → FAILED | 5.1 RED → 5.2 |
| Provider subprocess | (c) oversized output truncated + marker | 5.1 RED → 5.3 |
| Provider subprocess | (d) non-zero exit → FAILED | 5.1 RED → 5.2 |
| Gateway effect | (a) reject → Send never called | 4.1 RED + 8.5 |
| Gateway effect | (b) approve → exactly one Send | 8.6 |
| Gateway effect | (c) no token → refuses | 6.1 RED → 6.2 |
| Gateway effect | (d) inline token rejected at load | 1.6 + 6.1 |
| Gateway effect | (e) disallowed role → REJECTED, zero sends, no escalation | 4.1 RED + 8.4 |
| Gateway effect | (f) caller-supplied recipient ignored | 6.1 RED + 8.7 |

All 10 applicable threat-matrix cases mapped. No `N/A` rows omitted by mistake.

---

## Unmapped Spec Coverage Check

All 9 capability specs checked. No requirement left without a task:
- `agent-supervisor`: 5 requirements, all mapped to PRs 3–4 + 8.
- `provider-adapter`: 5 requirements, all mapped to PRs 2 + 5.
- `a2a-transport`: 5 requirements, all mapped to PR 3.
- `risk-policy-engine`: 5 requirements, all mapped to PR 4.
- `approval-flow`: 5 requirements, all mapped to PRs 4 + 8.
- `claude-code-adapter`: 5 requirements, all mapped to PR 5.
- `telegram-gateway`: 5 requirements, all mapped to PRs 6 + 8.
- `monitoring-ui`: 4 requirements, all mapped to PR 7.
- `company-as-code`: 4 requirements, all mapped to PRs 1 + 8.
