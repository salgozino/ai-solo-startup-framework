# Proposal: Agent Startup Framework — Foundation

> **Delivery note**: `delivery_strategy: auto-chain`, `review_budget: 400 lines`. Implementation
> will be split into chained PR slices. Slice planning belongs to `sdd-tasks`, not here.

**Change:** agent-startup-framework-foundation
**Date:** 2026-08-29
**Status:** Proposal — ready for spec phase
**Protocol baseline:** A2A spec v1.0.0 (Linux Foundation, Apache 2.0) — https://a2a-protocol.org/latest/

---

## Intent

Deploy startups run **entirely by AI agents**, in any domain (not only software). A human defines
the company as a YAML file, the framework materializes it: each role agent gets a supervised
process, an A2A endpoint, and a risk policy. The human interacts only with the CEO agent and
approves risk escalations through a minimal monitoring UI. No persistent provider sessions, no
trust-all-agents topology, no single-tenant shortcuts.

**Why now, why not AI Maestro:** AI Maestro (762 stars, actively maintained) addresses the same
space but with a fundamentally different model — persistent tmux sessions, no multi-tenancy,
no action authorization, no approval workflow, no company-as-code. The supervisor/ephemeral-provider
model, multi-tenancy, and risk escalation required here are architectural incompatibilities, not
configuration gaps. Verdict (verified): build independently, interoperate at protocol level only.

**What is genuinely new vs. A2A + AI Maestro:**

| Differentiator | AI Maestro | Plain A2A | This Framework |
|----------------|-----------|-----------|----------------|
| Agent process model | Persistent provider (tmux) | Not specified | Lightweight supervisor + ephemeral provider per task |
| Multi-tenancy | Absent | `tenant` field provided | First-class from day one — all storage keyed by full address |
| Action authorization + risk policy | Absent | Auth schemes only | Framework-owned policy engine on top of A2A identity |
| Human approval flow | Absent | `INPUT_REQUIRED`/`AUTH_REQUIRED` (mechanics only) | Risk policy + approval UI + state routing |
| Provider agnosticism | Shallow (tmux hands-off) | Not addressed | Explicit adapter contract, designed before first adapter |
| Company as code | Absent (UI-only) | Not addressed | Declarative YAML; CLI materializes it; reviewable in PR |

---

## Confirmed Product Decisions

> Decisions 1–12 source: Engram observation #1115. Decision 13 added after.
> Protocol supersessions applied: AMP → A2A, AID → A2A Agent Card + standard auth. AAP retained
> for human approval UI surface only (optional).

| # | Decision | Protocol mechanic |
|---|----------|------------------|
| 1 | Protocol core + provider-agnostic adapter contract. Claude Code is the FIRST adapter, not the runtime. Adapter contract designed before the first adapter. | A2A wire (consumed); adapter interface (built) |
| 2 | Autonomous agents; risky external actions escalate to human via CEO. Risk policy is configurable. | **`TASK_STATE_INPUT_REQUIRED` only.** VERIFIED in the A2A Go binding: it parks the task so an escalation survives a process restart, whereas `TASK_STATE_AUTH_REQUIRED` keeps execution alive and is out of v1 scope. Framework owns the risk policy and routes escalations. |
| 3 | CEO decomposes and delegates; role agents then talk **peer-to-peer** as **independent opaque peers**. Monitoring must capture P2P traffic. A2A's "not a sub-agent protocol" non-goal does NOT apply here — role agents are peers, not sub-agents. | A2A `SendMessage` / task model between independent A2A Servers |
| 4 | Long-lived lightweight SUPERVISOR per agent owns identity, A2A endpoint, queue, and memory. Provider is invoked **per task** (ephemeral). "Idle" is a real supervisor state. | Supervisor is the A2A Server; provider is opaque internals |
| 5 | Multi-tenancy as **architecture constraint** from day one — NOT v1 build scope. Retrofitting is the failure mode to avoid. | A2A `tenant` field (opaque, on every request); URL + auth routing; all storage keyed by full A2A address |
| 6 | Authorization: framework defines action policy on top of A2A identity and auth. AAP never provided this. Reframed: A2A security schemes (OAuth2, mTLS, OIDC, API key) provide identity and auth; the framework owns the action policy layer. | A2A `securitySchemes` in Agent Card; policy engine (built) |
| 7 | Monitoring UI: read-only state + approve/reject on risk escalations. No pause/kill/reassign (CEO-only rule). | `TaskState` enum + `ListTasks` + SSE/webhook push; approval via AAP optional for web surface |
| 8 | Declarative company file is source of truth. CLI materializes it. Company as code, reviewable in a PR. | Not protocol-level; framework-owned |
| 9 | Gateways are OUTBOUND ONLY — no inbound surface. **Telegram outbound IS in v1 scope** to prove real external communication; email outbound is deferred. Discord/Slack later. | Framework-owned outbound action, gated by the risk policy engine |
| 10 | User talks ONLY to the CEO agent — **and this extends outward**: since the v1 Telegram recipient is the owner, only the `ceo` role may send, or a worker would be talking to the human behind the CEO's back. Gateways are a company capability authorized by the risk policy, never an `agents[]` attribute. | Routing policy + `risk_policy.allowed_roles`; framework-owned |
| 11 | v1 runtime: CEO + 1 role agent on SAME machine, but transport is the real A2A transport on loopback — no localhost shortcuts, no in-process cheating. | A2A on loopback |
| 12 | v1 proof: trivial synthetic task loop demonstrating infrastructure (identity, transport, queue, task states, monitoring). Not a useful company yet. | A2A task lifecycle end-to-end |
| 13 | v1 INCLUDES a minimal monitoring UI showing agent state and task queue, with approve/reject on risk escalations. Overrides the first exploration's recommendation to defer the UI. | `TaskState` + push notifications + minimal web UI |

---

## Scope

### In Scope

- **Supervisor process**: A2A Server per agent — owns Agent Card, A2A endpoint, task queue, state machine, and supervisor lifecycle (IDLE → WORKING → escalation states).
- **Provider adapter contract**: Interface `invoke(task) → result`, `execute_action(action) → outcome`. Designed as an abstract contract before the Claude Code adapter is written.
- **Claude Code adapter**: First (only) concrete adapter in v1. Must not leak into the contract.
- **A2A transport on loopback**: Real A2A JSON-RPC (or gRPC) between CEO supervisor and worker supervisor. No in-process shortcuts.
- **Risk policy engine**: Configurable action-type → risk-level → escalation-or-permit rules. v1: flat config (no policy language).
- **Approval flow**: `TASK_STATE_INPUT_REQUIRED` as the escalation mechanic, and only that one. VERIFIED in the A2A Go binding: `INPUT_REQUIRED` ends execution and parks the task, so an escalation survives a process restart and a human can take hours to answer; `AUTH_REQUIRED` keeps the execution alive and is reserved for short out-of-band credential handoffs, out of v1 scope. Framework routes escalation to CEO, then to human.
- **Company-as-code CLI**: `company.yaml` declarative definition. CLI command materializes agents and wires A2A topology.
- **Minimal monitoring UI**: Web UI showing agent state, task queue. Approve/reject on pending risk escalations.
- **Telegram outbound gateway**: Outbound-only Telegram delivery to **the owner**. Declared once as a company-level capability — NOT an agent attribute. The risk policy is the single authorization mechanism, and in v1 it permits only the `ceo` role. Every permitted send is classified risky and escalates for human approval before delivery.
- **Synthetic task loop**: CEO receives a task → delegates to the worker over real A2A → the worker attempts a Telegram send and is **hard-denied by role** (no escalation offered, zero sends) → the worker returns its result normally → the CEO decides to notify the owner → the policy permits the role but classifies the action risky → escalates as `INPUT_REQUIRED` → human approves in the monitoring UI → the message is delivered → CEO completes and reports back. One coherent demo exercising transport, task states, role capability, risk approval, the UI, and a real external effect.
- **Multi-tenancy as design constraint**: All storage keyed by full A2A address (`agent.name/tenant`). No component may use agent-name alone as a lookup key.

### Out of Scope (v1)

- Remote / multi-machine deployment.
- Additional provider adapters (OpenCode, Codex, etc.).
- Inbound gateways (email, Telegram inbound) — no inbound surface at all.
- Email outbound gateway — deferred. Telegram outbound covers the external-communication proof in v1.
- Additional outbound gateways (Discord, Slack).
- Tenant administration UI.
- Role/permission administration UI.
- A2A custom bindings beyond the chosen protocol binding.
- Useful business output — v1 is demonstrable infrastructure.

**Ponytail justification for each in-scope item:**

| Item | Cannot defer because |
|------|---------------------|
| Supervisor + A2A on loopback | It IS the proof. Without it, v1 demonstrates nothing. |
| Adapter contract first | If Claude Code adapter is written first and core grows around it, the framework will claim to be provider-agnostic and will not be. |
| Risk policy + approval flow | The demo must show escalation. Without it, v1 is just a ping-pong task. |
| Telegram outbound gateway | Product decision: v1 must prove the framework can act on the outside world. It is also the most natural risky action available, so it exercises the escalation path end-to-end instead of requiring a fabricated one. Email is deferred because a second gateway proves nothing the first has not. |
| Monitoring UI (decision 13) | Explicitly included by product decision. Approve/reject requires a UI surface. |
| Company-as-code | Without it, "materializing a company" is undocumented manual steps. The YAML file is 10 lines; deferring costs more than building it. |
| Multi-tenancy as constraint | The A2A `tenant` field carries it for free; the cost of doing it right now is zero. The cost of retrofitting is a rewrite. |

---

## Capabilities

> Contract between proposal and specs phases. `openspec/specs/` is empty — all are new.

### New Capabilities

- `agent-supervisor`: Long-lived supervisor process per agent. Owns A2A Server, Agent Card, task queue, state machine, and provider lifecycle.
- `provider-adapter`: Abstract adapter contract (`invoke` / `execute_action`). Defines the seam between the framework core and any AI provider.
- `claude-code-adapter`: First concrete implementation of `provider-adapter` for Claude Code CLI.
- `a2a-transport`: A2A wire setup on loopback — Agent Card generation/signing, JSON-RPC or gRPC binding, task CRUD operations.
- `risk-policy-engine`: Action-type → risk-level rules. Escalation routing to CEO and through to human.
- `approval-flow`: `INPUT_REQUIRED` escalation state machine — never `AUTH_REQUIRED`. Human approve/reject surfaced through the monitoring UI; a rejected escalation produces no external effect and is distinguishable from an approved action that failed.
- `company-as-code`: `company.yaml` schema and CLI command to materialize agents, wire A2A topology, and apply risk policy config.
- `monitoring-ui`: Minimal web UI — agent state, task queue, approve/reject on risk escalations.
- `telegram-gateway`: Outbound-only Telegram delivery to the owner. A company-level capability, not an agent attribute. Reachable only by roles the risk policy permits (v1: `ceo` alone), classified as risky, and therefore delivered only after human approval. The recipient comes from configuration, never from the caller. No inbound handling.

### Modified Capabilities

None — this is the foundation change. No existing capabilities exist.

---

## Approach

A2A is a **dependency**, not a build item. This is the most important scope change from the first
exploration (where AMP would have required writing an AMP server from scratch). The framework
consumes the A2A SDK for the chosen stack.

**What the framework consumes (A2A):**

| A2A component | How consumed |
|--------------|-------------|
| SDK (Go / Python / TypeScript / Rust) | Imported — choice depends on stack decision (blocking design input) |
| Task model and `TaskState` enum | Used as-is: SUBMITTED → WORKING → INPUT_REQUIRED → WORKING → COMPLETED, with FAILED / REJECTED / CANCELED as terminals. `AUTH_REQUIRED` is not used in v1. |
| Agent Card + JWS signing | Supervisor generates and serves its own Agent Card |
| `SendMessage`, `GetTask`, `ListTasks`, `CancelTask`, `SubscribeToTask` | Core operations for supervisor-to-supervisor communication |
| Push notifications (SSE / webhook) | Used by monitoring UI to stream real-time state |
| `tenant` field + URL/auth routing | Multi-tenancy constraint satisfied via the A2A spec |
| Security schemes (OAuth2, mTLS, API key) | Auth between supervisors |
| In-Task Authorization (spec §7.6) | Escalation mechanics for approval flow |

**What the framework builds on top:**

| Framework component | Seam |
|--------------------|------|
| Supervisor process and lifecycle | A2A Server wraps into a managed process with IDLE/ACTIVE/RECOVERING states |
| Provider adapter contract | Abstract interface before first implementation |
| Action policy layer | Sits above A2A auth; classifies action types and routes escalations |
| Company-as-code CLI | Reads YAML, creates supervisors, registers Agent Cards |
| Monitoring UI | Consumes A2A `ListTasks` + push notifications; surfaces approve/reject |

**Loopback constraint (decision 11):** A2A runs on `localhost:<port>` with no TLS relaxation; standard A2A discovery and auth apply. Same config paths will work for remote deployment — no localhost-only assumptions in the design.

---

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `core/supervisor/` | New | Supervisor process, A2A Server, task queue, state machine |
| `core/adapter/` | New | Provider adapter contract (abstract interface) |
| `adapters/claude-code/` | New | Claude Code CLI adapter |
| `transport/a2a/` | New | A2A SDK setup, Agent Card, binding config |
| `policy/` | New | Risk policy engine and config schema |
| `cli/` | New | `company.yaml` schema, materialize command |
| `ui/monitoring/` | New | Minimal monitoring web UI |
| `gateways/telegram/` | New | Outbound-only Telegram gateway |
| `openspec/config.yaml` | Modified | Stack decision will update this when resolved |

> All paths are tentative — the stack is undecided. Final paths depend on the design-phase stack decision.

---

## Open Questions (Block Design Phase)

| Question | Why it blocks |
|----------|--------------|
| **Stack choice** | Every concrete path in "Affected Areas" depends on this. The design phase must decide before task planning can start. Constraints: A2A has official SDKs for Go, Python, TypeScript, and Rust. Supervisor daemon + CLI + web UI must all be feasible in the chosen stack. The first exploration's stack analysis is **partially obsolete** — it rated Go's ecosystem as "no support for these protocols." That is now false: `a2a-go` is an official SDK. The analysis must be redone against A2A SDK maturity. |
| **A2A protocol binding** | JSON-RPC, gRPC, or HTTP+JSON. Choice affects transport setup and SDK usage. Design-phase decision. |
| **Monitoring UI rendering strategy** | Served from supervisor binary vs. separate process vs. embedded SPA. Affects how the UI communicates with A2A push notifications. |

---

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| A2A `1.0.0` is recent — breaking changes in future minor versions | Low (Linux Foundation + major vendors = stability commitment) | Pin exact version `1.0.0` in lock files. Do not auto-upgrade. |
| Supervisor context rebuild cost per task | Med | Adapter contract must define context size budget. Design phase must specify limits. |
| Scope creep toward AI Maestro features (code graph, CozoDB memory, tmux viewer) | Med | Non-goals list is explicit. Do not build what AI Maestro already does well. |
| Monitoring UI in v1 increases scope risk | Med | UI is intentionally minimal — state list + approve/reject only. Resist richer UI until core is proven. |
| Telegram gateway introduces external credentials and a real network dependency into v1 | Med | Bot token handled as a secret, never in `company.yaml` in cleartext. Gateway must be behind the adapter-style seam so a failed send is a task failure, not a supervisor crash. Verification must not depend on a live Telegram account for unit-level tests. |
| Adapter contract written too late (after Claude Code adapter exists) | High if not enforced | Proposal explicitly blocks: adapter contract MUST be a spec and a design artifact before any adapter code is written. |
| Provider adapter Claude Code writes to the core (coupling leak) | Med | Contract is the seam. Spec review must enforce that nothing in `core/` imports from `adapters/`. |

---

## Rollback Plan

v1 is greenfield — no production data, no users, no deployed infrastructure. Rollback is:
1. Delete the repository.
2. The `company.yaml` and A2A Agent Cards are local files — no external state to unwind.

For future versions with real tenants: agent state is in the supervisor's local storage, versioned
with the company config. Rollback = revert the config commit, restart supervisors.

---

## Dependencies

- A2A Protocol spec v1.0.0 — https://a2a-protocol.org/latest/ (verified, Linux Foundation, Apache 2.0)
- A2A SDK for chosen stack (official, under https://github.com/a2aproject) — VERIFIED: Go, Python, TypeScript, Java, .NET, Rust all available
- Stack decision (blocking) — design-phase input
- Telegram Bot API — outbound only. Requires a bot token supplied as a secret, not committed to `company.yaml`.

---

## Success Criteria

- [ ] CEO supervisor and worker supervisor start, register Agent Cards, and are discoverable via A2A.
- [ ] CEO sends a task to the worker over A2A on loopback (real transport, not in-process).
- [ ] Worker's Claude Code adapter receives the task, processes it, and returns a result.
- [ ] The worker attempts a Telegram send and is hard-denied because its role is not in `allowed_roles` — the task is `REJECTED`, no message is sent, and **no approval is ever shown to the human**.
- [ ] The CEO attempts a Telegram send; the policy permits the role, classifies the action risky, and escalates it as `INPUT_REQUIRED`, surfaced in the monitoring UI.
- [ ] Human approves via the monitoring UI; the Telegram message is actually delivered to the owner; the CEO task resumes and completes.
- [ ] Rejecting the escalation prevents delivery — no Telegram message is sent.
- [ ] No agent can choose the recipient — the destination comes from configuration, never from the action intent.
- [ ] Task traverses at least these states: `SUBMITTED → WORKING → INPUT_REQUIRED → WORKING → COMPLETED`.
- [ ] Monitoring UI shows live task state and approve/reject control.
- [ ] All storage is keyed by full A2A address — no component uses agent-name alone as a lookup key (multi-tenancy constraint verified by code review).
- [ ] A second `company.yaml` with a different tenant name can be materialized alongside the first without interference.
- [ ] Adapter contract exists as a spec artifact before the Claude Code adapter is implemented.
