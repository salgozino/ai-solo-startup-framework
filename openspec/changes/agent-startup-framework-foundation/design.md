# Design: Agent Startup Framework — Foundation

> Input: `proposal.md`; Engram #1122 (A2A baseline — authoritative for protocol facts), #1126 (v1
> scope), #1115 (product decisions; its AMP/AID/AAP claims superseded by #1122). Delivery
> `auto-chain`, 400-line budget.

## Primary Decisions

| Decision | Choice | Reason |
|----------|--------|--------|
| **Stack** | **Go, single language, whole system** | Only candidate giving one static VPS binary for daemon+CLI+embedded UI, goroutine-per-task supervision, AND an official A2A SDK with a native ephemeral-`--exec` model. |
| **A2A binding** | **JSON-RPC/HTTP** on loopback, SSE for push | Human-readable during the loopback demo; SSE feeds the UI; sits behind `a2asrv` so it stays swappable. |
| **Monitoring UI** | **Embedded static assets** (`//go:embed`), authored as **plain HTML/CSS/JS — no build step, no node toolchain** | Zero extra process AND zero extra toolchain; consumes `ListTasks`+SSE like any A2A client. v1 UI is a state list plus approve/reject, which does not earn a framework. |
| **Architecture** | **Ports-and-adapters only at the two real seams** (Provider, Gateway); rest plain packages | Provider seam is the proposal's #1 risk → contract-first port; blanket hexagonal is over-engineering for one machine/tenant. |

## Verified vs Inferred

**VERIFIED** (repo/spec, Aug 2026):
- `a2a-go` official, **module v2**, requires **Go ≥1.25.0**, 445★/263 commits/active. Ships `a2asrv`
  (transport-agnostic handler), `a2aclient`, **gRPC+REST+JSON-RPC bindings**, pluggable auth
  middleware. https://github.com/a2aproject/a2a-go
- `a2a-go` CLI `a2a serve --exec "./script"` **exposes a local script as an A2A agent** — the
  supervisor/ephemeral-provider pattern is already idiomatic. Same README.
- SDK adoption: Python 2114★, JS 598★, Java 481★, **Go 445★**, .NET 257★, Rust 68★.
  https://github.com/a2aproject
- `TaskState`, Agent Card+JWS, `tenant` field, In-Task Auth §7.6, SSE/webhook push — spec v1.0.0 (#1122).

**VERIFIED — `a2a-go` v2 API surface** (raw source on `main`, cross-checked against pkg.go.dev v2.5.0; full detail in Engram #1136):
- `Tenant string \`json:"tenant,omitempty"\`` is the first field of every request struct in `a2a/core.go`
  (`SendMessageRequest`, `GetTaskRequest`, `ListTasksRequest`, `CancelTaskRequest`,
  `SubscribeToTaskRequest`, `GetExtendedAgentCardRequest`), and is also on `AgentInterface` in
  `a2a/agent.go`. `NewAgentInterface()` does NOT set it — assign explicitly when building a card.
- `a2aclient.NewFromCard` propagates it automatically (`tenantTransportDecorator`). Precedence:
  explicit `req.Tenant` > `AgentInterface.Tenant` > ambient `a2a.TenantFrom(ctx)`. Opt-out via
  `Config.DisableTenantPropagation`. Server side it lands as `ExecutorContext.Tenant`; also
  `CallContext.Tenant()` for interceptors. Helpers: `a2a.TenantFrom`, `a2a.AttachTenant`.
- `a2a.TaskStateInputRequired` and `a2a.TaskStateAuthRequired` both exist; `TaskState.Terminal()` is
  false for both. But `internal/taskupdate/final.go` `IsFinal()` treats `InputRequired` as final and
  `AuthRequired` as NOT final — the two states have **different execution semantics** (see Approval Flow).
- `AgentExecutor` is a push-iterator: `Execute(ctx, execCtx) iter.Seq2[a2a.Event, error]`. Transitions
  are signalled by yielding `a2a.NewStatusUpdateEvent(execCtx, state, msg)`. There is **no `resume()` API**.
- **No SDK type exists for an in-task authorization challenge payload.** `a2a/auth.go` holds only Agent
  Card security schemes. Spec §7.6.4 deliberately leaves credential representation undefined.

**INFERRED**: binding, embedded-assets choice, seam scope, supervisor state machine, key format, P2P-vs-broker call.

## Technical Approach

One Go module → one binary that is both supervisor daemon and `company.yaml` CLI. Each agent is a
goroutine-hosted `a2asrv` handler on its own loopback port, serving its own signed Agent Card. The
provider runs as an ephemeral child process per task via `os/exec`. The UI is hand-written HTML/CSS/JS,
`//go:embed`-ed and served on the CEO's HTTP mux — no build step and no node toolchain. A2A is consumed,
never reimplemented.

## Architecture Decisions

**Go (whole system).** Rejected: Python (best SDK, but no single-artifact story, clumsy subprocess
supervision), TypeScript (good UI, weak daemon/single-binary), Go-daemon + TS-UI split (doubles
toolchain for a UI capped at "state list + approve/reject"). Go alone satisfies all four hard
constraints at once. `ponytail`: one language = one build/lockfile/gravity well.

**JSON-RPC binding.** Rejected gRPC (opaque on loopback, protoc toolchain) and REST (fine, but JSON-RPC
is the most `curl`-debuggable method surface). Behind `a2asrv`, so config not lock-in.

**Embedded static assets, hand-written, no build step.** Go does not render UI — it only *carries* the
assets. `//go:embed` is a distribution decision, not a UI-language one: one binary ships to the VPS with
no `node_modules`, no second process, no asset server. Rejected: a separate UI process (extra deployable,
contradicts "minimal"); no UI at all (violates decision 13); **Next.js** (brings its own server, routing
and SSR that the Go server already provides — either two processes, breaking the single-binary property,
or a static export using Next purely as a React build tool, paying full complexity for none of it);
**React+Vite** (legitimate — static build embeds fine and preserves one binary — but it adds a node
toolchain and a second build stage for a UI capped at "state list + approve/reject"). Plain HTML/CSS/JS
with `EventSource` covers the v1 scope in a few hundred lines. Visual quality comes from CSS, not from a
framework. **This choice is contained and reversible**: the seam is "static files under `ui/`", so
adopting React later changes nothing on the Go side.

**Ports only at real seams.** Ports for `Provider` and `Gateway` (real second implementations coming);
supervisor/policy/transport are plain packages. A port with one forever-implementation is waste;
skipping the Provider port re-creates the #1 risk.

## Data Flow — Synthetic Loop

    Human ─task▶ CEO ─A2A SendMessage (loopback JSON-RPC)▶ Worker
                                                            ▼
                                          Worker invokes Provider (os/exec, ephemeral)
                                                            │
                                       ┌────────────────────┴────────────────────┐
                                       │ (negative path)                          │ (happy path)
                            Worker emits telegram_send intent          Worker returns result
                            Policy: role 'engineer' ∉ allowed_roles              │
                            ⇒ HARD DENY, no escalation offered                   │
                            ⇒ task REJECTED, zero sends                          │
                                                                                 ▼
                                                        CEO wants to notify the owner
                                                        Policy: role 'ceo' allowed, risk=risky
                                                        ⇒ escalate
                 ┌──────────── INPUT_REQUIRED (SSE) ──────────────────────────────┘
                 ▼
           Monitoring UI ── approve / reject ──▶ CEO
              approve │ reject
                 ▼        ▼
     Telegram Gateway   no send (unreachable: no token minted)
       (to OWNER)
                 │
                 ▼
      CEO task resumes ─▶ COMPLETED ─▶ CEO reports to Human

## Supervisor Lifecycle & State Machine

Supervisor = long-lived A2A Server owning AgentID/Agent Card, endpoint, queue, memory. Provider is
invoked ephemerally per task, never exposed on the wire.

**Supervisor states** (framework-owned): `STARTING → IDLE ⇄ WORKING → IDLE`; `IDLE → DRAINING →
STOPPED`; crash → `RECOVERING → IDLE`. **IDLE is real**: server up, queue empty, no child running.

**Per-task → A2A `TaskState`:**

| Moment | `TaskState` |
|---|---|
| Queued | `SUBMITTED` |
| Provider child running | `WORKING` (`RUNNING` = streaming sub-steps) |
| Risky action, awaiting human approval | **`INPUT_REQUIRED` only** — never `AUTH_REQUIRED` (see Approval Flow) |
| Approved, resume | `WORKING` |
| Done | `COMPLETED` |
| Non-zero exit / send failure | `FAILED` |
| Policy hard-deny / human cancel | `REJECTED` / `CANCELED` |

**Crash/restart**: state persisted locally keyed by full A2A address; `RECOVERING` replays open tasks;
a dead child is re-invoked from last persisted task state, not resumed in place.

**Context**: `{task input, minimal role-memory slice, prior resolutions}`, assembled fresh per
invocation and **capped by a declared `context_budget`** on the `Provider` port; over-budget truncates
oldest-first with a marker, never silent.

## Provider Adapter Contract (BEFORE the Claude Code adapter)

```go
// core depends on THIS, never on any adapter package
type Provider interface {
    Invoke(ctx context.Context, in TaskInvocation) (ProviderResult, error)
    ExecuteAction(ctx context.Context, act Action) (ActionOutcome, error) // policy-approved effects only
    Capabilities() ProviderCapabilities                                    // context_budget + action kinds
}
type TaskInvocation struct {
    Address A2AAddress      // full address, never agent-name alone
    Input   []a2a.Part
    Context BoundedContext  // pre-capped
    Resume  *ResumePoint    // set when resuming from INPUT_REQUIRED
}
type ProviderResult struct {
    Parts         []a2a.Part
    ActionIntents []ActionIntent // "wants to send X" — classified by policy, NOT executed here
    Status        TerminalOrEscalated
}
```

**Leak vectors → guards:**

| Leak | Guard |
|---|---|
| Session | Provider stateless; continuity in framework `Context`/`ResumePoint`. No session id in port. |
| Tool permissions | Provider never self-authorizes; returns `ActionIntent`, policy decides, only approved `Action` returns via `ExecuteAction`. |
| Output format | Port speaks A2A `Part`, not CLI stdout; adapter parses internally. |
| Streaming | Optional sub-updates on adapter's type, not a required port method. |
| Exit codes | Adapter maps non-zero → `error`; core sees `FAILED`. |
| Context injection | Supervisor assembles `BoundedContext`; adapter receives, never builds. |
| Cost/limits | `context_budget`+`Capabilities()` on port; provider rate limits stay inside adapter as `error`. |

**Claude Code adapter** (satisfies port): wraps `claude` CLI via `os/exec`, fresh child per `Invoke`;
injects `BoundedContext`; parses output → `[]a2a.Part`; maps exit codes; emits `ActionIntent`. In
`adapters/claudecode/`, imported only by the composition root, never by `core/`.

## Risk Policy — Enforcement, not Notification

Declared in `company.yaml` as `action_kind → {risk, allowed_roles}` (v1: no policy language). Every
`ActionIntent` is classified in **two stages**:

1. **Capability** — is the emitting agent's role in `allowed_roles`? If not: **hard deny**. The task goes
   to `REJECTED`. No escalation is offered, because offering a human an approval for something the role
   may never do would train the human to rubber-stamp.
2. **Risk** — if the role is allowed and the risk is `risky`, the task enters `INPUT_REQUIRED`, routed
   CEO → UI, and waits for a verdict. Non-risky allowed actions execute directly.

The gateway is reachable **only** via `ExecuteAction` holding a policy-minted approval token. **Deny or
reject mints no token → the send is unreachable → the effect cannot occur.** There is no code path from a
denied or rejected intent to a live gateway call. Enforcement is by construction, not by an `if`: an
approval check can be dropped in a refactor, a dependency that cannot be constructed cannot.

**Approval flow — uses `INPUT_REQUIRED`, never `AUTH_REQUIRED`.** VERIFIED in `a2a-go`: the two states
are NOT interchangeable.

| | `INPUT_REQUIRED` | `AUTH_REQUIRED` |
|---|---|---|
| `IsFinal()` | **true** — execution ends | false — execution continues |
| Executor goroutine | returns and dies | **stays alive** holding the pause |
| Resume | new `SendMessageRequest` carrying `Message.TaskID`; `ExecutorContext.StoredTask` non-nil marks it a resume | credential arrives out-of-band, no client message needed |
| Survives process restart | **yes** — task is parked in the store | no — pause lives in a goroutine |

Our escalation is "park the task, human approves whenever, then resume", so it MUST be `INPUT_REQUIRED`.
Choosing `AUTH_REQUIRED` would couple approval latency to a live in-process goroutine and to
`WithAgentInactivityTimeout` — a defect invisible in development that appears under slow approval or
restart, i.e. only in production. `AUTH_REQUIRED` is reserved for short out-of-band credential handoffs
and is out of v1 scope.

**Approval-request payload is ours to define.** The SDK has no in-task authorization challenge type
(spec §7.6.4 leaves it undefined). We carry a versioned schema of our own in `TaskStatus.Message` parts;
because `a2a.Data` round-trips as `any`, we validate it on both ends.

CEO surfaces pending escalations via `ListTasks`+SSE; the UI posts the verdict; the supervisor resumes
the task with a new `SendMessage` (approve) or drives it to `REJECTED` (reject).

## Peer-to-Peer with Monitoring Visibility

**Choice: direct P2P + mandatory self-reporting, NOT a broker/tap.** Tradeoff: a broker makes
observation trivial but adds a central bottleneck and contradicts decision 3 (independent opaque peers);
direct P2P keeps faithful topology but the monitor trusts each supervisor's report. **Justification**:
the product decision is P2P; a broker would make the monitored topology a lie. Each supervisor emits its
own task events; the monitor aggregates via `ListTasks`/SSE. Trust is bounded — v1 supervisors are all
ours on one machine. `ponytail`: no broker to build/run/scale.

## Multi-Tenancy Addressing

`A2AAddress = "{agent-name}/{tenant}"`; the A2A `tenant` field populates the second segment on every
request. **All** storage (queue, memory, task state, decisions) keyed by full address. `A2AAddress` is
the only lookup-key type; agent-name alone is never a map key (enforced by review + a test that a second
tenant's `company.yaml` materializes without collision). v1 = one tenant, but nothing assumes it.

**Two VERIFIED caveats from the SDK:**
1. `Tenant` is `omitempty`, so an empty tenant is indistinguishable from an absent one. Since our key's
   second segment must never be empty, a `CallInterceptor` rejects `req.Tenant == ""` at the edge.
2. The tenant is **client-asserted, not authenticated.** It is a routing and partition key only, never
   an authorization boundary on its own. Authorization pairs it with `CallContext.User`. Writing this
   down because treating a client-supplied string as a security boundary is the classic multi-tenant
   breach.

Server side the value arrives as `ExecutorContext.Tenant`; `a2aclient.NewFromCard` propagates it
outbound automatically, so the spec §8.3.2 client MUST is satisfied without custom transport code.

## company.yaml (~10 lines)

```yaml
tenant: acme
agents:
  - { name: ceo,    role: ceo,      provider: claude-code }
  - { name: worker, role: engineer, provider: claude-code }
gateways:
  telegram:
    token_env: TELEGRAM_BOT_TOKEN       # secret via env, never inline
    recipient_env: TELEGRAM_OWNER_ID    # v1 recipient is the OWNER; no third-party recipients
risk_policy:
  telegram_send:
    risk: risky            # risky -> escalate to the human before the effect occurs
    allowed_roles: [ceo]   # any other role: hard deny, no escalation is even offered
```

**Gateways are a company-level capability, not an agent attribute.** There is deliberately no
`agents[].gateways` field. Reason: `risk_policy` is already the authorization mechanism (it is what mints
the approval token that makes the gateway reachable at all). A per-agent allowlist would be a SECOND
authorization mechanism answering the same question, and two mechanisms drift — someone adds a role,
updates one, forgets the other. `allowed_roles` on the policy is the single source of truth.

**Recipient is the owner (v1).** Telegram is the company-to-owner channel. This is what keeps decision 10
("the human interacts only with the CEO") intact: if a worker could message the owner directly, it would
be talking to the human behind the CEO's back. Hence `allowed_roles: [ceo]`. Third-party recipients are
a future extension and would reopen this decision — a support agent messaging a customer is not the same
question and must not inherit this answer by default.

Placement is implicit in v1 (all loopback); schema leaves room for `placement:` later without a rewrite.

## Telegram Outbound Gateway Seam

`Gateway` port (`Send(ctx, msg) (Receipt, error)`), outbound only, reached only via approved
`ExecuteAction`. **Recipient in v1 is the owner**, resolved from `recipient_env` — the port takes no
caller-supplied destination, so no agent can redirect a message to an arbitrary chat. Token from
`token_env` at runtime, never in `company.yaml`; the config loader rejects an inline token. Failed send →
`error` → task `FAILED`, never a supervisor panic. **No live account for units**: a `fakeGateway`
implementing the port + a **contract test** asserting the real adapter and fake agree (message shape,
error-on-failure, receipt). The live HTTP call runs only in an opt-in, env-gated integration test.

## Testing Strategy (`go test`, stdlib only)

| Layer | What | Tooling |
|---|---|---|
| Unit | policy classification, address keying, transitions, context bounding | stdlib `testing` |
| Contract | `Provider`/`Gateway` fake vs real adapter agree | table tests on the port |
| Integration | supervisor over **real A2A JSON-RPC on loopback** (no in-process shortcut); opt-in live Telegram (env gate) | real listener + `a2a-go` |
| E2E | full synthetic loop plus two negatives: **reject → zero sends** and **disallowed role → hard deny, zero sends, no escalation raised** (fake asserts `Send` never called in both) | one `go test` driving two supervisors |

`ponytail`: no assertion/mock framework. Threat-matrix RED tests authored before production code.

## Threat Matrix (Applicable — subprocess + process integration)

| Boundary | Cases | Applicability | Response | RED tests |
|---|---|---|---|---|
| Documentation-like paths | executable-looking provider output/args | **N/A**: framework never classifies/executes files by extension; output → `Part`s. | — | — |
| Git repo / commit / push / PR | — | **N/A**: no VCS/PR automation in v1. | — | — |
| **Provider subprocess** | argv injection into `os/exec`, hostile/oversized output, non-zero exit, hung child | **Applicable** | argv slice never shell string; `ctx` deadline kills hung child; output size-capped pre-parse; non-zero → `FAILED`. | (a) argv-as-slice no interpolation; (b) hung child killed → `FAILED`; (c) oversized output truncated w/ marker; (d) non-zero → `FAILED`. |
| **Gateway effect** | reach `Send` without token; unauthorized role escalating; caller-chosen recipient; cleartext token in config | **Applicable** | `Send` only via `ExecuteAction` with policy-minted token; role checked against `allowed_roles` BEFORE risk, hard-deny never escalates; recipient from `recipient_env`, never from the caller; token from `token_env`. | (a) reject → `Send` never called (E2E neg); (b) approve → exactly one `Send`; (c) no token → refuse; (d) inline-token config rejected at load; (e) disallowed role → `REJECTED`, zero sends, **no escalation raised**; (f) intent carrying its own recipient → recipient ignored, owner used. |

Applicable rows carry into `tasks.md` unchanged; RED before production.

## Migration / Rollout

None — greenfield. Rollback per proposal: delete repo; all state is local files.

## Directory / Module Structure

    /                     (one Go module, go ≥1.25)
    ├── cmd/company/      composition root: CLI + daemon entrypoint
    ├── core/
    │   ├── supervisor/   A2A Server, queue, state machine, context assembly
    │   ├── port/         Provider + Gateway interfaces — no adapter imports
    │   ├── policy/       risk classification + approval-token minting
    │   └── address/      A2AAddress + tenant keying (only lookup-key type)
    ├── adapters/claudecode/   Claude Code CLI adapter (imports port; imported only by cmd/)
    ├── gateways/telegram/     outbound adapter + fakeGateway (imports port)
    ├── transport/a2a/    a2a-go wiring: Agent Card, JSON-RPC, SSE
    ├── ui/               //go:embed hand-written HTML/CSS/JS — no build step, no node_modules
    │                     (state list + approve/reject, live via EventSource)
    └── config/           company.yaml schema + loader (rejects inline secrets)

**Hard rule**: nothing in `core/` imports `adapters/` or `gateways/`; `cmd/company/` is the only place
concretes wire to ports.

## Slice Boundaries (note only — `sdd-tasks` plans slices)

Roughly: (1) module + `address` + `config` loader; (2) `port` + `Provider` contract tests w/ fake; (3)
`transport/a2a` + supervisor state machine on loopback; (4) `policy` + approval token; (5) Claude Code
adapter; (6) Telegram gateway + contract test; (7) embedded UI; (8) E2E loop incl. negative case.

## Open Questions

- [x] **RESOLVED** — `a2a-go` v2 API surface verified (Engram #1136). `tenant` is first-class end to end,
  no design change needed. In-Task Auth states both exist but are **not interchangeable**; the approval
  flow was corrected to `INPUT_REQUIRED` only, and the challenge payload is ours to define.
- [ ] `RECOVERING` store: stdlib (JSON files keyed by address) vs embedded KV. Lean stdlib per
  `ponytail` unless recovery needs transactions — decide in slice 1.
- [ ] **Go toolchain is not installed on the development machine.** `a2a-go` v2 requires Go ≥1.25.0.
  Blocks implementation, not spec or task planning.
