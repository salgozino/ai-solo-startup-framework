# Exploration: AI Maestro Prior-Art / Build-vs-Buy Evaluation

**Change:** agent-startup-framework-foundation
**Date:** 2026-08-28
**Status:** Complete — verdict ready for proposal phase
**Exploration type:** Prior-art / competitive analysis (research only, no code)
**Artifact store:** hybrid (OpenSpec + Engram)

---

## Lead Finding — Verdict First

**Verdict: Interoperate at the protocol level only. Build independently.**

The single strongest piece of evidence: AI Maestro's internal messaging stack is **not** AMP. It is a bespoke file-based system (`~/.agent-messaging/messages/inbox/<session>/`) with tmux notifications — exactly as the AAP spec hinted. AMP is listed as an aspirational external communication channel, not the internal bus. The core product does not implement the protocol suite it authored. This means the protocols and the product are separable: you can implement AMP/AID natively without taking any dependency on AI Maestro.

The secondary reason: AI Maestro has no concept of multi-tenancy, action authorization, risk escalation, or identity-gated approvals. These are not gaps that a fork would fill easily; they require architectural decisions that conflict with AI Maestro's single-user, same-machine, trust-all-agents model.

---

## 1. What AI Maestro Actually Is

> VERIFIED: https://ai-maestro.23blocks.com, https://github.com/23blocks-OS/ai-maestro, https://github.com/23blocks-OS/ai-maestro-plugins, https://github.com/agentmessaging

### 1.1 Core Product Description

AI Maestro is a **Next.js web dashboard** (served at `http://localhost:23000`) that manages multiple AI coding agents running as **tmux sessions** on one or more machines. Its primary value is:

- **Visual multiplexing**: See and switch between many tmux sessions from a browser tab.
- **WebSocket terminal proxying**: Streams `xterm.js` output per session via WebSocket.
- **File-based inbox**: A shared `~/.agent-messaging/messages/` directory with JSON files per session, plus a REST API to write/read them.
- **tmux notifications**: Shell commands that inject `tmux display-message` or `tmux send-keys` to alert a session.
- **Peer mesh for multi-machine**: Each AI Maestro instance can register peers (other machines running the same server); the dashboard proxies WebSocket connections to remote tmux sessions through the peer chain.
- **CozoDB memory**: Semantic memory per agent backed by an embedded graph database.
- **Code Graph**: Codebase dependency visualization via `ts-morph`.

**What it is NOT**: a supervisor process, a lifecycle manager for AI providers, an authorization server, or a multi-tenant platform. Agents are tmux sessions. "Running an agent" means starting a tmux session that runs Claude Code, Aider, or any other terminal tool.

### 1.2 Licensing

**VERIFIED: https://github.com/23blocks-OS/ai-maestro/blob/main/LICENSE**

The entire `ai-maestro` repository is MIT licensed. The AMP protocol repo (`agentmessaging/protocol`) is Apache 2.0. `agentmessaging/agent-identity` (AID) is MIT. `agentmessaging/agent-actions` (AAP) is MIT.

**Everything is open source and forkable. There is no closed/commercial component in the public repos.**

### 1.3 What is Installed by the One-Line Installer

VERIFIED from README: `curl -fsSL .../remote-install.sh | sh` installs:
1. The AI Maestro Next.js server and dashboard.
2. The AMP `claude-plugin` (shell scripts in `~/.local/bin/`: `amp-send`, `amp-inbox`, `send-tmux-message.sh`).
3. A Claude Code plugin with 5 skills (agent-messaging, agent-identity, agent-management, memory-search, code-graph, docs-search, planning).

**Important**: The "plugin-only" install (no service) only makes the `planning` skill functional. The other 5 skills call `http://localhost:23000/api/*` — they require the AI Maestro service to be running.

---

## 2. Architecture — What Actually Runs

### 2.1 Agent Process Model

VERIFIED from `docs/CONCEPTS.md` and `docs/AGENT-COMMUNICATION-ARCHITECTURE.md`:

```
AI Maestro Server (Next.js at localhost:23000)
├── /api/messages       — File-based inbox REST API
├── /api/agents         — Agent registry (tmux session list)
├── /api/hosts          — Peer mesh registration
└── WebSocket bridge    — xterm.js ↔ PTY ↔ tmux session

Storage:
~/.agent-messaging/messages/inbox/<session>/msg-*.json
~/.agent-messaging/messages/sent/<session>/msg-*.json

CLI tools (shell scripts):
amp-send    → POST /api/messages
amp-inbox   → GET  /api/messages?agent=<session>
send-tmux-message.sh → tmux display-message -t <session>
```

There is **no supervisor process per agent**. An agent IS a tmux session. The AI provider (Claude Code) runs inside that session as a long-running interactive process. State survives between tasks because the session stays open — it is not restarted per task.

**Key implication**: AI Maestro's model is `persistent-provider` (Claude Code stays running in tmux), not `ephemeral-provider-per-task` as the framework's product decision #4 specifies. These are fundamentally different lifecycle models.

### 2.2 Internal Messaging — NOT AMP

VERIFIED from `docs/AGENT-COMMUNICATION-ARCHITECTURE.md` (read in full, 1038 lines):

AI Maestro's internal messaging stack is:
- **Channel 1**: REST API + JSON files at `~/.agent-messaging/messages/`. Message IDs are `msg-{timestamp}-{random}`. No Ed25519 signing. No priority-based routing. No federation. Address format is the tmux session name (plain string, no `@scope.provider`).
- **Channel 2**: `tmux display-message` / `tmux send-keys` for real-time notifications.
- **Channel 3**: Slack Bridge (external).

AMP (`agentmessaging.org`) is listed as a **future/external communication channel** — the `channels/` directory in the repo and the `chore(channels): release amp channel plugin 0.1.1` commit (Aug 28, 2026) confirm that AMP is being added as an optional channel, not the existing backbone.

**The protocols (AMP/AID) are authored by the same person but are NOT the internal implementation of AI Maestro's messaging.** The product and the protocols are being developed in parallel, not from the same codebase.

### 2.3 Multi-Machine

VERIFIED from `docs/CONCEPTS.md`:

Peer mesh works by: each AI Maestro node exposes `POST /api/hosts/register-peer` and `POST /api/hosts/exchange-peers`. Nodes proxy WebSocket connections to remote sessions through the HTTP/WebSocket peer chain. Transport is plain HTTP/WebSocket, secured optionally via Tailscale VPN. **No AMP routing. No AID authentication between peers.**

### 2.4 The Dashboard

VERIFIED from `docs/CONCEPTS.md`, `docs/AGENT-COMMUNICATION-ARCHITECTURE.md`:

The dashboard is a standard Next.js SPA served at `localhost:23000`. It has:
- Session list in sidebar (3-level hierarchy from tmux name: `project-subproject-agent`).
- xterm.js terminal per session (read/write).
- MessageCenter component (inbox/compose UI for the file-based messages).
- Agent notes editor.
- Code graph visualization.
- Memory search.
- Settings (peer management).

**There is no approval/rejection workflow in the dashboard.** No escalation UI. No risk-pending state display. No concept of a human approving an agent action.

### 2.5 AMP Integration Status (Critical Finding)

VERIFIED from commit log (https://github.com/23blocks-OS/ai-maestro/commits/main/):

```
Aug 28, 2026: chore(channels): release amp channel plugin 0.1.1 (v0.36.38)
Aug 28, 2026: feat(delivery): wake-adapter chain, idle gating, and retry
Aug 28, 2026: fix(delivery): never report a message wake we cannot prove
```

The `channels/` directory and these commits show that AMP is being integrated as **a delivery channel alongside the existing file-based system**, not as a replacement. It is labeled `amp channel plugin 0.1.1` — a plugin, not core infrastructure.

From `agentmessaging` org (https://github.com/agentmessaging):
- `reference-server`: 0 stars, 0 forks — explicitly listed as "Coming Soon."
- `sdk-typescript`: 0 stars, 0 forks — "Coming Soon."
- AI Maestro is listed as the **only** AMP provider in the providers table.

---

## 3. Provider Support

VERIFIED from https://ai-maestro.23blocks.com (homepage) and https://github.com/23blocks-OS/ai-maestro/blob/main/README.md:

The homepage lists: **Claude Code, Codex, Aider, Cursor, OpenClaw, Hermes, Droid, "Any Agent."**

The technical mechanism is: any terminal-based AI that can run inside a tmux session works. AI Maestro does not invoke the AI provider — it just manages the tmux session. The provider runs autonomously inside the session.

**Provider agnosticism is shallow**: it works because AI Maestro does not touch the provider at all. There is no adapter abstraction, no context injection, no output parsing, no task lifecycle management. The human (or another agent via `amp-send`) puts text into the session; the AI responds; that's the integration.

For the framework's purposes (ephemeral provider per task, adapter contract, context injection), this is the opposite of what is needed.

---

## 4. Maintenance and Viability

VERIFIED from commit log and GitHub metadata:

| Metric | Value | Source |
|--------|-------|--------|
| Stars | 762 | https://github.com/23blocks-OS/ai-maestro |
| Forks | 100 | Same |
| Open issues | 13 | Same |
| Open PRs | 0 | Same |
| Total commits | 1,075 | Same |
| Current version | v0.37.6 | Commit Aug 29, 2026 |
| Commits in last 10 days | ~20+ | Aug 19–29, 2026 visible on first page |
| Contributors | 1 active (jpelaez-23blocks) | All commits show same 3 avatars: Juan, a co-author, and Claude |
| Watchers | 14 | GitHub |

**Assessment**:
- **Active**: Multiple commits per week, daily cadence visible on Aug 27–29.
- **Single contributor**: Every commit is co-authored by Juan Pelaez + Claude Code. No external contributors have merged PRs. 0 open PRs.
- **Rapid iteration**: v0.36.x to v0.37.x in a short window, with multiple patch releases per day.
- **Abandonment risk**: MODERATE. One author, no bus factor. However, velocity is high and the project is clearly in active use by the author (he runs 80+ agents per his README). If Juan stops, the project stops.
- **Fork viability**: High. MIT license, clean Next.js codebase, well-documented architecture.

---

## 5. Gap Analysis Against the Thirteen Product Decisions

> Legend: ✅ Provided | 🟡 Partial | ❌ Not provided | VERIFIED = read from source | INFERRED = judgment

| # | Decision | AI Maestro Provides | Evidence | Gap |
|---|----------|---------------------|----------|-----|
| 1 | Protocol core + provider-agnostic adapter contract | 🟡 Partial — no adapter contract; provider agnosticism is via tmux (crude) | VERIFIED: CONCEPTS.md, arch doc | Framework must design adapter interface; AI Maestro's approach (hands-off tmux) is not portable to ephemeral-provider model |
| 2 | Autonomous agents with risk escalation to human CEO | ❌ None | VERIFIED: no escalation UI in dashboard, no pending-action state in any API | Must be built entirely by the framework |
| 3 | CEO decomposes, agents talk P2P, monitoring captures P2P traffic | 🟡 Partial — file-based inbox enables P2P messages; monitoring shows message center but not live traffic | VERIFIED: arch doc shows MessageCenter reads inbox files; no broker tap | P2P is possible; live monitoring tap is not |
| 4 | Long-lived lightweight SUPERVISOR per agent (provider invoked per task) | ❌ None — opposite model | VERIFIED: agents ARE tmux sessions with persistent Claude Code | Fundamental architectural incompatibility |
| 5 | Multi-tenancy as architecture constraint from day one | ❌ None | VERIFIED: session names are plain strings, no scope/tenant partitioning | Single-user/single-company by design |
| 6 | AgentIDs verifiable identity + message signing + authorization | 🟡 Partial — AID shell scripts exist (CLI for Ed25519 keys, OAuth token exchange) | VERIFIED: agentmessaging/agent-identity — bash scripts, no open server | No AID server; authorization layer absent; must implement OAuth server |
| 7 | Monitoring UI: read-only state + approve/reject risk escalations | 🟡 Partial — read-only terminal view exists; NO approve/reject | VERIFIED: dashboard has MessageCenter, no approval workflow | Approval/rejection UI and state machine must be built |
| 8 | Declarative company file (company as code) | ❌ None | INFERRED from absence in README, docs, and codebase structure | Must be built; AI Maestro is entirely imperative (UI-driven) |
| 9 | Outbound gateways (email, Telegram) — v1 out of scope | 🟡 Partial — Slack/email gateways exist | VERIFIED: https://github.com/23blocks-OS/aimaestro-gateways mentioned | Gateway pattern exists; not Telegram-specific; v1 deferred anyway |
| 10 | User talks ONLY to CEO agent | ❌ None | INFERRED: dashboard exposes all agents equally to the human; no CEO-exclusive interface | CEO-only routing must be framework-defined |
| 11 | v1 same-machine, transport must be cross-machine compatible from day one | 🟡 Partial — file-based inbox is localhost-only; peer mesh exists for multi-machine | VERIFIED: peer mesh in CONCEPTS.md | Peer mesh is HTTP, not AMP; framework must use AMP on loopback to satisfy the "same transport" requirement |
| 12 | v1 proof: synthetic task loop demonstrating identity, transport, queue, states, monitoring | 🟡 Partial — AI Maestro has all the infrastructure but the proof is "open tmux, start Claude Code" | VERIFIED: QUICKSTART.md | Framework's synthetic loop must exercise supervisor states, which AI Maestro does not model |
| 13 (NEW) | Minimal monitoring UI showing agent state, task queue, approve/reject risk escalations (AAP) | 🟡 Very partial — dashboard shows terminal output only; no state machine, no task queue, no approval flow | VERIFIED: dashboard components in arch doc | The entire approval/risk-escalation surface must be built |

**Verdicts by category**:
- Provided or partial: decisions 1, 3, 6, 7, 9, 11, 12, 13
- Not provided at all: decisions 2, 4, 5, 8, 10

The two most disqualifying gaps are:
1. **Decision 4 (supervisor model)**: AI Maestro's architecture is the opposite. Changing it requires replacing the core.
2. **Decision 5 (multi-tenancy)**: Not present anywhere in the design; would require a rewrite of the storage layer and address model.

---

## 6. Verdict with Reasoning

### Recommendation: Interoperate at the protocol level only. Build independently.

**Reasoning**:

**Why not "build on it"**:
- The supervisor model (decision 4) is architecturally incompatible. AI Maestro's agents are persistent tmux sessions; the framework's agents are lightweight supervisors that invoke ephemeral providers. You cannot build one on top of the other — they are different models of what "an agent" is.
- Multi-tenancy (decision 5) is not in the design. Adding it to AI Maestro would require changing the storage layer, the address model, and every API endpoint. That is a fork, not an extension.
- There is no approval/escalation workflow (decisions 2, 7, 13). Adding this to AI Maestro requires new data models, new UI components, and new protocol semantics — none of which align with AI Maestro's current architecture.

**Why not "fork it"**:
- The useful parts of AI Maestro (the dashboard UI, the code graph, the CozoDB memory) are all built around the tmux-session model. Forking and rewriting the agent model would leave you with a Next.js UI shell and a lot of dead code.
- The protocol implementations (AID shell scripts, AMP shell scripts) are simpler to reimplement from spec than to extract from the plugin system.
- Forking creates a maintenance burden: tracking upstream changes while diverging on core architecture.

**Why "interoperate at the protocol level"**:
- The AMP spec (Apache 2.0), AID spec (MIT), and AAP spec (MIT) are usable without taking any runtime dependency on AI Maestro.
- The framework implements its own AMP provider (local) and AID OAuth server (minimal for v1). This is required regardless — there is no open-source AMP provider other than AI Maestro itself.
- If AI Maestro adds an open AMP provider server later (it is listed as "Coming Soon"), the framework's implementation can be replaced or federated.

**What would have to be true for "build on it" to win**:
- AI Maestro would need to adopt the supervisor/ephemeral-provider model as a first-class concept.
- AI Maestro would need to implement multi-tenancy.
- AI Maestro would need to expose an approval/escalation API.
- None of these appear in the roadmap, backlog, or open issues.

---

## 7. Differentiation — What This Framework Offers That AI Maestro Does Not

These are real, evidenced differences — not invented justifications:

| Differentiator | AI Maestro | This Framework |
|----------------|-----------|----------------|
| Agent process model | Persistent provider in tmux (heavy, provider-coupled) | Lightweight supervisor + ephemeral provider per task (provider-agnostic, crash-survivable) |
| Multi-tenancy | Not present; single-user design | Architecture constraint from day one; AMP address encodes tenant as scope |
| Action authorization | Not present | AID scopes repurposed as action authorization; policy engine per agent |
| Risk escalation flow | Not present | State machine: PENDING → APPROVED/REJECTED; routed via AMP to CEO and human |
| Company as code | Not present; UI-only configuration | Declarative YAML company file; materialized by CLI; reviewable in PR |
| Monitoring UI (approval surface) | Read-only terminal viewer | Read-only state + task queue + approve/reject AAP-speaking UI |
| Provider adapter contract | Not present; tmux is the "adapter" | Explicit interface: `invoke(task)→result`, `execute_action(action)→outcome`; pluggable |
| Outbound gateways | Slack/Email exist but not Telegram | v1 deferred; gateway-agent pattern designed into topology from start |
| Same transport loopback→multi-machine | File-based (localhost-only) and HTTP mesh (multi-machine) are different transports | AMP on loopback for v1; same transport scales to cross-machine without redesign |

**Are you about to rebuild an existing thing for no reason?**

Partially yes, partially no.

- The **tmux session dashboard** and **code graph** and **CozoDB memory** parts of AI Maestro are genuinely useful and would be redundant to rebuild. These are scope you should NOT build.
- The **supervisor model**, **multi-tenant AMP provider**, **AID OAuth server**, **action authorization**, **risk escalation**, and **company-as-code** parts do not exist in AI Maestro at all. These are the core of what this framework is.

The honest framing: this framework is to AI Maestro what a bank's core ledger system is to Mint.com. Mint shows you your balance nicely. The ledger handles identity, authorization, and settlement. They are adjacent but different products.

---

## 8. Risks

1. **Protocol drift**: AMP is `0.1.2-draft`, AID is `0.3.0` (no explicit draft label but single-author). The framework should pin to specific spec versions. VERIFIED risk from first exploration: breaking changes have occurred between 0.1.x versions.

2. **AMP provider gap**: AI Maestro is the only AMP provider. The reference server (`agentmessaging/reference-server`) has 0 stars, 0 commits visible, and is listed as "Coming Soon." The framework must implement its own provider. This is significant implementation effort for v1.

3. **Single-author ecosystem**: All three protocols plus AI Maestro plus the reference server are Juan Pelaez / 23blocks. If 23blocks pivots, the entire protocol ecosystem could stall. Mitigation: protocols are simple enough to self-implement; MIT/Apache-2.0 license allows forking the specs themselves.

4. **AI Maestro community as first users**: With 762 stars and 100 forks, AI Maestro has real users. If the framework surfaces in that community, users may ask "how is this different from AI Maestro?" The differentiation in section 7 is the answer — but it must be communicated clearly.

5. **Scope creep toward AI Maestro features**: The temptation to add a code graph, CozoDB memory, or "nice dashboard" features is real. Resist until the core (supervisor, AMP provider, AID server, escalation flow) is proven.

---

## 9. What to NOT Build (Ponytail Constraint)

Applying the laziness ladder to "things AI Maestro already does well":

| Feature | AI Maestro quality | Recommendation |
|---------|-------------------|----------------|
| tmux session viewer | Excellent | Do not rebuild. If a monitoring UI is needed, consider embedding the AI Maestro dashboard or linking to it. |
| Codebase code graph | Good | Out of scope for framework v1. |
| CozoDB semantic memory | Good | Out of scope for framework v1. |
| Agent personality library (150+ markdown files) | Exists externally | Reuse `msitarzewski/agency-agents` directly if needed. |
| Slack/Email gateways | Exists | v1 deferred; when building, evaluate wrapping the existing `aimaestro-gateways` repo first. |

---

## 10. Ready for Proposal

**YES.**

The orchestrator should tell the user:

> AI Maestro is a real, actively maintained (762 stars, multiple commits per week), MIT-licensed product. It is the reference implementation for AMP/AID/AAP. However, the core of AI Maestro is a tmux dashboard — not a supervisor framework, not a multi-tenant platform, and not an action authorization system. The architecture is incompatible with the framework's product decisions on three counts: the agent model (persistent vs. ephemeral), multi-tenancy (absent vs. day-one constraint), and action authorization (absent vs. core feature).
>
> The recommendation is to **interoperate at the protocol level only** (use the AMP/AID/AAP specs, build your own implementations) and **build the framework independently**. The differentiation is real: supervisor model, multi-tenancy, action authorization, and company-as-code are all absent from AI Maestro and central to this framework.
>
> Decision 13 (monitoring UI with AAP-speaking approve/reject) has no analog in AI Maestro and must be built. AAP v1.0 is suitable for the human approval surface (as the first exploration established).
>
> The only honest warning: the parts of AI Maestro you should NOT rebuild (tmux viewer, code graph, memory) are things you might want anyway. Scope discipline is the risk.
