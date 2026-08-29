# Exploration: agent-startup-framework-foundation

**Change:** agent-startup-framework-foundation
**Date:** 2026-08-28
**Status:** Complete — ready for proposal phase
**Artifact store:** hybrid (OpenSpec + Engram)

---

## 1. Protocol Maturity Verdicts

> Lead finding. Read this before anything else.

### 1.1 AMP — Agent Messaging Protocol

- **URL verified:** https://agentmessaging.org / https://github.com/agentmessaging/protocol
- **Version:** 0.1.2-draft (labelled "draft"; last updated 2026-02-07)
- **Owner:** 23blocks (Juan Peláez, Boulder CO). One org, one author.
- **Stars:** 32. Forks: 5. Issues: 3 open. PRs: 0.
- **Reference implementation:** CLI scripts (Bash) + an NPX plugin for Claude Code. No library SDK, no standalone server binary. The "provider" side is not open source — only client scripts are.
- **Spec completeness:** HIGH relative to its maturity. 10 spec files covering identity, registration, messages, routing, federation, local networks, security, API, external agents, local bus. Message format, signing procedure, routing algorithm are normatively specified with MUSTs.

**VERDICT: A real, substantive draft spec with real normative text. It is NOT vaporware. It IS early-stage, single-author, and the server side is not open source. Adopting it means implementing your own provider.**

### 1.2 AID — Agent Identity Protocol (agentids.org)

- **URL verified:** https://agentids.org / https://github.com/agentmessaging/agent-identity
- **Version:** v0.3.0 (website badge). README does not label itself a draft.
- **Owner:** Same org as AMP — 23blocks / agentmessaging. Shared GitHub org.
- **Stars:** 4. Forks: 1. Issues: 0. PRs: 0.
- **Reference implementation:** Bash CLI scripts (`aid-init`, `aid-register`, `aid-request`, `aid-token`, `aid-status`, `aid-discover`). The auth server is referenced as "23blocks Authentication API" (separate private repo). No open-source server.
- **Spec completeness:** MEDIUM-HIGH. The protocol is fully described on the website and in the README. No separate numbered spec files (unlike AMP). The normative content is the README. Standards referenced are real (RFC 6749, RFC 7515/7519, RFC 7662, RFC 8414, RFC 8628, RFC 8693, RFC 9728, RFC 8785).

**Key normative requirements (VERIFIED from README/site):**
- Agent MUST have an Ed25519 keypair. Private key never leaves the agent's machine.
- Registration requires human admin approval. Agents CANNOT self-register with elevated permissions.
- Token exchange uses custom grant type `urn:aid:agent-identity`.
- Proof of possession: `"aid-token-exchange\n{unix_timestamp}\n{oidc_issuer}"`, signed with Ed25519. 5-minute validity window.
- Agent Identity Document is a signed JSON blob with `aid_version`, `address`, `public_key`, `key_algorithm`, `fingerprint`, `issued_at`, `expires_at`, `signature`.
- Canonical identifier SHOULD be a `did:key` (recommended default), or `did:web`. NOT blockchain-anchored DIDs.
- Agent lifecycle: `pending → active ↔ suspended → deleted`. Auth server MUST support these states.
- Token introspection (RFC 7662) MUST return `agent_status` field.

**VERDICT: A real protocol with real OAuth 2.0 foundations. More a "design document + CLI tools" than a formal spec. The server side is not open source. Building on it means implementing an AID-compatible OAuth server, or running against 23blocks' hosted service. The hosted service is the only known reference implementation.**

### 1.3 AAP — Agent Actions Protocol

- **URL verified:** https://agentactions.org / https://github.com/agentmessaging/agent-actions
- **Version:** v1.0 (labelled current, not draft)
- **Owner:** Same org — 23blocks / agentmessaging. 3 commits total in the repo.
- **Stars:** 3. Forks: 0. Issues: 0. PRs: 0.
- **Reference implementation:** AI Maestro (23blocks' commercial product). No standalone open-source server.
- **Spec completeness:** LOW-MEDIUM. The normative content is entirely on the agentactions.org landing page plus 7 spec files in the repo (01-introduction through 07-implementers). The actual protocol is extremely thin.

**What AAP actually is (VERIFIED from site + GitHub):**
AAP is NOT a general-purpose agent action authorization protocol. It is a **UI interaction capture protocol** — specifically for when an AI agent renders an HTML canvas page in an iframe, and a human user clicks buttons on it. The protocol captures those clicks as JSON records and notifies the agent.

The full protocol is:
1. Agent writes an HTML page with `maestro.send(action, element, data)` calls.
2. A dashboard renders it in a sandboxed iframe and injects a bridge script.
3. User clicks → bridge calls `window.parent.postMessage({type: "canvas:interaction", action, element, data}, "*")`.
4. Dashboard catches postMessage → `POST /api/agents/:id/canvas/interactions` → writes JSON file → sends terminal notification.
5. Agent reads the JSON file.

There is NO concept of action authorization, permissions, approval-required states, or risk escalation in AAP v1.0. The roadmap mentions bidirectional push in v1.1 and acknowledgment in v1.2 — both unimplemented.

**VERDICT: AAP is NOT what the product decisions assumed it was. It is a UI event-capture protocol, not an agent action authorization framework. The name is misleading. There is no notion of "risky actions," "pending/deferred states," or "gating by AgentIDs identity" in the current spec. The authorization and risk-escalation concerns from the product decisions must be built by the framework — AAP provides no help there.**

---

## 2. Data Models / Message Shapes (for design reference)

### AMP Message Envelope (VERIFIED: spec/04-messages.md)

```json
{
  "envelope": {
    "version": "amp/0.1",
    "id": "msg_<unix_timestamp>_<random>",
    "from": "agent@tenant.provider",
    "to": "agent@tenant.provider",
    "subject": "string (max 256 chars)",
    "priority": "urgent|high|normal|low",
    "timestamp": "ISO 8601",
    "expires_at": "ISO 8601 (optional)",
    "signature": "base64-encoded Ed25519 signature",
    "in_reply_to": "msg_id or null",
    "thread_id": "msg_id",
    "idempotency_key": "idk_<uuid> (optional)"
  },
  "payload": {
    "type": "request|response|notification|alert|task|status|handoff|ack|update|system",
    "message": "string (max 64 KB)",
    "context": { "arbitrary": "object, max 256 KB" },
    "attachments": []
  }
}
```

Signing canonical string: `{from}|{to}|{subject}|{priority}|{in_reply_to}|{payload_hash}`
where `payload_hash = Base64(SHA256(JSON.stringify(payload, sort_keys=True)))`.

### AMP Address Format (VERIFIED: spec/02-identity.md)

```
<agent-name>@<scope>.<provider>
# Example (local deployment):
ceo@acme.aimaestro.local
worker@acme.aimaestro.local
```

The `.local` TLD is reserved for air-gapped / local network deployments. The framework SHOULD use `*.aimaestro.local` or a custom local domain for v1.

### AID Agent Identity Document (VERIFIED: agentids.org)

```json
{
  "aid_version": "1.0",
  "address": "agent@tenant.local",
  "public_key": "-----BEGIN PUBLIC KEY-----\n...",
  "key_algorithm": "Ed25519",
  "fingerprint": "SHA256:abc123...",
  "issued_at": "ISO 8601",
  "expires_at": "ISO 8601",
  "signature": "base64url-ed25519-signature"
}
```

### AAP Interaction Record (VERIFIED: agentactions.org)

```json
{
  "id": "uuid",
  "timestamp": "ISO 8601",
  "canvasFile": "relative/path.html",
  "action": "click|submit|change|select|toggle|dismiss|navigate|custom",
  "element": "element-id (optional)",
  "data": { "arbitrary": "payload" },
  "summary": "human-readable string"
}
```

---

## 3. Transport Analysis (VERIFIED)

### AMP Transport (spec/05-routing.md)

AMP is NOT transport-agnostic. It specifies four delivery methods in priority order:
1. **Mesh** (HTTP forwarding between hosts on `.local` network)
2. **WebSocket** (real-time push, authenticated via first frame)
3. **Webhook** (HTTP POST to agent's registered URL, 3 retries then falls back to relay)
4. **Relay** (queue, 7-day TTL, poll via `GET /v1/messages/pending`)

For same-machine v1: messages would use **local** delivery (filesystem-based). The spec's local bus (spec/10-local-bus.md) covers intra-entity communication.

**Provider side** = REST API (`POST /v1/route`, `GET /v1/messages/pending`, `DELETE /v1/messages/pending/:id`, WebSocket at `wss://host/v1/ws`). The framework must implement this server interface to host a local provider.

### AID Transport (VERIFIED: agentids.org)

AID is HTTP-only (standard OAuth 2.0 over HTTPS). Endpoints:
- `POST /agent_registrations` — admin-initiated registration
- `POST /agent_registrations/request` — agent-initiated (creates pending)
- `POST /oauth/token` — token exchange with `grant_type=urn:aid:agent-identity`
- `POST /oauth/introspect` — real-time status check (RFC 7662)
- `GET /.well-known/jwks.json` — JWKS endpoint
- `GET /.well-known/oauth-authorization-server` — discovery (RFC 8414)

For loopback v1, this is HTTP over localhost (no TLS required but the spec says HTTPS — accept self-signed or disable TLS validation for local dev).

### AAP Transport (VERIFIED: agentactions.org)

AAP uses `window.parent.postMessage` (browser iframe to parent window), then `POST /api/agents/:id/canvas/interactions` from the dashboard to a local API. This is a browser-dashboard protocol. For a CLI-only v1 with no browser, AAP is **not applicable unless the monitoring UI is a web app**.

---

## 4. Cryptography Summary (VERIFIED)

| Protocol | Signing | Key Type | Identity |
|----------|---------|----------|----------|
| AMP      | Ed25519 message signing | Ed25519 keypair per agent | Address + public key registered at provider |
| AID      | Ed25519 identity signing + PoP | Same Ed25519 keypair (shared with AMP) | `did:key` (recommended) derived from public key |
| AAP      | None | None | None (no crypto in AAP v1.0) |

**Key insight:** AMP and AID share the same Ed25519 keypair. One `~/.agent-messaging/` directory. The supervisor's keypair IS the agent's identity across both protocols.

---

## 5. Authorization Analysis

### What the specs provide (VERIFIED)

**AID:** Role-based authorization scopes baked into the OAuth token. An agent can only request scopes that its assigned role allows. Scope intersection at token-request time. Admin controls role assignment. Admin can suspend agent (instant kill). This covers API-level authorization: "which OAuth-protected APIs can this agent call."

**AMP:** Communication ACLs (allowlist-based, wildcard matching) per agent. Controls which agents can message which agents. NOT action-level authorization.

**AAP:** No authorization concept whatsoever in v1.0.

### What the framework must invent (INFERRED)

The product decisions describe:
- "risky external actions (money, external comms, publishing, deletion) escalate to human via CEO"
- "Authorization: which AAP actions each identity may execute"

Neither AMP nor AID nor AAP provides:
- A concept of "risky action" classification
- An approval-required / pending state for actions (not messages)
- A mechanism to pause an agent's action pending human approval
- A policy engine for "agent X may not execute action type Y without escalation"

**The framework must build its own action authorization layer.** AID's role/scope model could be repurposed — if "actions" are modeled as OAuth scopes and the supervisor requests a token scoped to only the permitted action types — but that's an extension not described in the spec. More likely: the framework needs a policy engine that the supervisor consults before executing any action flagged as risky.

---

## 6. Fit Against Product Decisions (Decision #1115)

### 6.1 Supervisor Model ↔ AMP/AID Identity

**Finding (VERIFIED):** AMP's identity model is: one Ed25519 keypair per agent, stored locally in `~/.agent-messaging/`. The address is `agent-name@scope.provider`. AID reuses the same keypair.

**The supervisor model is COMPATIBLE.** The supervisor owns the keypair and the address. When the AI provider (Claude Code) is invoked per task, it runs as a subprocess with access to the supervisor's filesystem state — it reads the keypair and sends messages on the supervisor's behalf. The identity belongs to the process that holds the key file, which is the supervisor's working directory. There is no concept in either spec of "the identity must belong to the runtime that calls the LLM." The signing is done by whoever holds `private.pem` — and that's the supervisor.

**No conflict.** The design decision is spec-compatible.

### 6.2 Provider Agnosticism ↔ Adapter Contract

**Finding (INFERRED):** The adapter must abstract:
1. **Task dispatch:** How the supervisor invokes the provider (Claude Code: `claude -p <prompt>` via CLI; OpenCode: similar; Codex: `openai api`). This is a process-execution interface, not protocol.
2. **Context passing:** The provider session has no memory of the agent's AMP identity or task queue. The supervisor must inject context into every task invocation (the IDENTITY.md pattern from spec/02-identity.md solves this for AMP addresses).
3. **Output capture:** Provider output is text/tool-calls. The supervisor parses this to determine: (a) did the task complete? (b) did it request an action that needs escalation?
4. **Action execution:** The supervisor, not the provider, executes actions with real side effects. The provider proposes; the supervisor executes (or escalates).

**Leak risks:** If the adapter passes raw provider output directly to AMP message payloads without normalization, provider-specific formatting leaks into the wire format. The adapter contract must define a normalized "task result" structure that the supervisor serializes to AMP messages.

### 6.3 Peer-to-Peer Agent Traffic ↔ Monitoring

**Finding (VERIFIED from AMP spec):** AMP routing goes: sender agent → provider's `/v1/route` → delivery to recipient. In the local deployment scenario (same machine), the local bus (spec/10-local-bus.md — not fully read) handles intra-entity communication. Either way, all messages transit through the local provider.

**AMP offers NO observer/relay/tap mechanism.** There is no "CC the monitor" field. There is no subscription-to-all-messages API for observers.

**Two realistic approaches:**

| Approach | How | Observation | Tradeoff |
|----------|-----|-------------|----------|
| **Broker (logging middleware)** | The local provider implementation logs all routed messages before delivery. The monitor reads the log. | Faithful — sees every message. | Central bottleneck; provider code must be written by the framework. |
| **Agent self-reporting** | Each agent, after sending/receiving a message, also sends a `notification` type message to a well-known monitor address. | Near-faithful — depends on agent compliance. | No broker needed; but P2P messages may arrive at monitor out of order and agents could omit reports. |

**Recommendation (INFERRED):** For v1 (CEO + 1 worker, same machine), broker is the right choice. The framework owns the local provider implementation, so it controls the logging path. P2P self-reporting is appropriate for cross-machine federation where the framework doesn't own the provider.

**There is no "easy" path here — the spec does not help with observability. The framework must build it.**

### 6.4 Authorization: AAP Actions ↔ AgentIDs

**Finding (VERIFIED):** As established above, AAP does NOT define action authorization. The product decisions stated "Authorization: which AAP actions each identity may execute" — this was based on an assumption that AAP has an authorization layer. It does not.

**What can be done:** The AID role/scope model can be used to authorize "action classes." For example, an agent's AID role could have scopes like `email:send`, `file:delete`, `payment:initiate`. Before the supervisor executes such an action, it checks whether the current agent's AID token includes the required scope. If not, escalation to CEO. If yes but risk policy says escalate anyway, escalation to human.

**This is framework-invented behavior, not something AID/AAP specify.** The design phase must define this policy model.

### 6.5 Risk Escalation ↔ Protocol Support

**Finding (VERIFIED + INFERRED):**
- AMP has no notion of "pending action" or "approval-required" state. It is a messaging protocol.
- AID has no "pending action" concept either — its `pending` state is for agent *registration*, not for actions.
- AAP v1.0 has no bidirectional flow (v1.1 roadmap item).

**What a risk escalation looks like with the current specs:**
1. Worker agent wants to execute a risky action.
2. Worker supervisor detects the action type is in the risk policy.
3. Worker supervisor sends an AMP `task` or `alert` message to the CEO agent: "I need to execute [action X]. Approve or reject."
4. CEO agent (or the monitoring UI) receives the message and presents it to the human.
5. Human approves via the monitoring UI (which would use AAP's `maestro.send` if the UI is a web canvas).
6. CEO agent sends an AMP `response` message back to the worker: "approved" or "rejected."
7. Worker supervisor proceeds or aborts.

**This is entirely buildable with AMP messages. But the protocol is silent on it — the framework implements all the semantics.** The AMP message types (`task`, `alert`, `response`, `ack`) are just labels; the framework defines the meaning.

### 6.6 Multi-Tenancy as Design Constraint

**Finding (VERIFIED):** AMP's address format is `agent@scope.provider`. The `scope` is the tenant. The `provider` is the domain. In a local deployment: `ceo@acme.aimaestro.local`. Multi-tenancy is inherent in the address format — different scopes are different tenants.

**What must be tenant-scoped from day one:**
- The local provider's identity registry (one registry namespace per tenant, keyed by scope)
- The message relay queue (one queue per agent address, which includes scope)
- The AID auth server's role and registration storage (one namespace per tenant)
- The monitoring UI state (one view per tenant/company)
- The declarative company file (one file per company, maps to one tenant scope)

**The AMP address format already encodes tenancy. As long as the framework routes by full address (not just agent-name), multi-tenancy is free.** The risk is if any component uses agent-name alone as a lookup key — that breaks multi-tenancy immediately.

### 6.7 Outbound Gateways (Email, Telegram)

**Finding (INFERRED):** AAP is a browser-to-agent UI protocol — it has nothing to do with email or Telegram. Outbound gateways are not addressed by any of the three protocols.

**Options:**
1. Model them as AID-scoped actions: the agent's role includes `email:send` scope. The supervisor validates scope before calling the email service.
2. Model them as dedicated "gateway agent" services: the CEO or worker sends an AMP message to a gateway agent (`email-gateway@acme.aimaestro.local`) which executes the actual send. The gateway agent itself requires an AID token scoped to the email service.

**Option 2 is architecturally cleaner for multi-tenancy and audit trails. It is also more complex for v1.** For a v1 synthetic task, outbound gateways are out of scope per the product decisions (the task is synthetic). If the framework scaffolds the gateway agent pattern now, it can add real integrations later without redesigning the topology.

---

## 7. Ponytail Audit: What Can Be Deferred?

> Applying the laziness ladder to the product decisions.

| Constraint | Load-bearing for v1? | Verdict |
|------------|---------------------|---------|
| Multi-tenancy as architecture constraint | **YES** — the address format encodes it for free; doing it wrong now means a rewrite | Keep. Cost is zero if using full AMP addresses everywhere. |
| AgentIDs identity (Ed25519 keypair) | **YES** — without it, messaging has no authentication | Keep. It's a keypair + a few bash scripts. Deferring it means v1 is unauthenticated, which defeats the demo goal. |
| AMP transport (loopback) | **YES** — it IS the proof | Keep. Without it, the infrastructure demo fails. |
| AID OAuth server (full implementation) | **NO for v1** — a dev-mode server that hardcodes one tenant + one agent is enough to prove the flow | **DEFER** full multi-tenant AID server. v1 needs just enough to issue a token and verify it. |
| AAP canvas UI for approvals | **NO for v1** — CLI-based approval (`read -p "Approve? [y/n]"`) proves the escalation flow without a web browser | **DEFER** AAP entirely for v1. Replace with a simple CLI prompt or a terminal notification. |
| Monitoring web UI | **NO for v1** — a log file or a `tail -f` of the broker log proves observability | **DEFER** monitoring web UI. v1 just needs logged messages, not a dashboard. |
| Declarative company file | **YES as constraint** — but the v1 file can be trivially simple (5 lines of YAML) | Keep the pattern; defer richness. |
| Provider agnosticism / adapter contract | **YES as design constraint** — but v1 implements only Claude Code adapter | Keep the interface; defer other adapters. |
| Outbound gateways (email, Telegram) | **NO for v1** — synthetic task needs no real outbound | **DEFER entirely.** |
| Risk policy engine | **YES** — the demo must show escalation | Keep. But v1 can be a simple allowlist in a config file, not a policy language. |

**Net result from ponytail audit:** AAP is deferrable for v1. The monitoring UI is deferrable. The full AID OAuth server is deferrable (use a minimal dev server). The risk policy can be a flat config, not an engine. These three deferrals reduce v1 scope significantly without losing any of the core demonstrable value.

---

## 8. Stack Options

> The stack is UNDECIDED. No recommendation is made here — that belongs to design/user.

### Option A: Go — single binary daemon

**What:** Go supervisor daemon (one binary), SQLite for state, chi or standard net/http for AMP+AID HTTP endpoints, `gorilla/websocket` for WS delivery, a React/TS SPA served from the binary for the monitoring UI.

| Dimension | Assessment |
|-----------|-----------|
| Single-binary distribution | Excellent — `go build` produces one binary, CGO-free if using pure-go SQLite |
| Concurrency model | Excellent — goroutines per supervisor + per-connection; natural fit for concurrent agents |
| AMP/AID ecosystem | None — must implement from scratch; no Go AMP SDK exists |
| Monitoring UI story | Embed SPA as `//go:embed` assets; thin REST API; acceptable |
| CLI story | Excellent — cobra for CLI commands |
| Team familiarity risk | High if team is not Go-fluent; low if they are |
| Deployment | Single binary + `systemd` unit. Trivial for a VPS. |

**When to choose:** Team knows Go. Want a single deployable artifact. Comfortable building the AMP provider from first principles.

### Option B: Node.js/TypeScript — monorepo

**What:** TypeScript monorepo. Supervisor as a long-running Node process (`tsx` or compiled to `node`). Express/Fastify for HTTP. `ws` for WebSocket. `better-sqlite3` for state. A Next.js or Vite SPA for monitoring UI. `pm2` or `systemd` for process management.

| Dimension | Assessment |
|-----------|-----------|
| Single-binary distribution | Poor — requires Node runtime; `pkg` or `bun compile` can help but add complexity |
| Concurrency model | Adequate — event loop per process; multi-supervisor deployment needs `cluster` or separate processes |
| AMP/AID ecosystem | Small advantage — 23blocks' reference implementations are partially in JS; Claude Code plugin is NPX-based |
| Monitoring UI story | Excellent — React/Next.js is natural; shared types across frontend and backend |
| CLI story | Good — `commander` or `yargs` |
| Team familiarity risk | Low for most web developers |
| Deployment | Requires Node installed on VPS; `pm2` for process management |

**When to choose:** Team is JS/TS-fluent. Want maximum ecosystem alignment with 23blocks' existing tools. Willing to accept the runtime dependency.

### Option C: Python — supervisor scripts + FastAPI

**What:** Python supervisor as `asyncio` process. FastAPI for AMP/AID HTTP endpoints. `aiohttp` or `websockets` for WS. `sqlite3` (stdlib) for state. Streamlit or a minimal HTML/HTMX page for monitoring. `click` for CLI.

| Dimension | Assessment |
|-----------|-----------|
| Single-binary distribution | Poor — requires Python runtime; `pyinstaller` works but fragile |
| Concurrency model | Adequate — asyncio; process-per-supervisor model is natural |
| AMP/AID ecosystem | Slight advantage — 23blocks' spec pseudocode is in Python; Ed25519 via `cryptography` lib |
| Monitoring UI story | Streamlit is fast to build but limited; HTMX is minimal but sufficient |
| CLI story | Excellent — `click` or `typer` |
| Team familiarity risk | Low for Python developers; high if team is not Python-fluent |
| Deployment | Requires Python on VPS; `poetry` + `venv` adds setup steps |

**When to choose:** Team is Python-fluent. Want to prototype fast. Comfortable with the deployment overhead.

---

## 9. Explicit Gaps the Framework Must Fill

| Gap | Spec silent on | Framework must invent |
|-----|---------------|----------------------|
| Action authorization | All three specs | Policy engine: action type → risk level → escalation or permit |
| Approval-required state for actions | All three specs | State machine: PENDING_APPROVAL → APPROVED/REJECTED for agent-proposed actions |
| P2P traffic observability | AMP | Broker tap in the local provider, OR mandatory self-reporting by all agents |
| Risk policy configuration | All three specs | Declarative policy file (which action types require human approval) |
| Provider adapter contract | None of the specs | Interface: `invoke(task) → result`, `execute_action(action) → outcome` |
| Outbound gateways | None | Gateway agent pattern (v2+) or direct API call (v1) |
| Tenant isolation in state storage | AMP addresses it at protocol level | Implementation must key all stored state by full address, never by agent-name alone |
| Human-readable monitoring without AAP | AAP is inapplicable without a browser | Log-based or TUI-based monitoring for v1 |

---

## 10. Risks

1. **Single-author protocol risk.** All three protocols are from one org (23blocks / Juan Peláez). If 23blocks pivots or abandons the specs, the framework is built on orphaned protocols. Mitigation: the specs are simple enough that the framework fully implements them in-house; there is no dependency on 23blocks infrastructure.

2. **AID server absence.** There is no open-source AID-compatible OAuth server. The framework must implement one or use 23blocks' hosted service (which adds cloud dependency and is inapplicable for local/VPS deployment). For v1, a minimal dev-mode OAuth server is feasible but requires implementation effort.

3. **AMP "draft" status.** The spec is explicitly labelled `Status: Draft`. Breaking changes between 0.1.x versions have occurred (see version history in spec/01-introduction.md). The framework should pin to a specific spec version and not auto-update.

4. **AAP misalignment.** The product decisions assumed AAP covers action authorization. It does not. The entire action-authorization and risk-escalation layer must be designed from scratch. This is likely the largest scope surprise from this exploration.

5. **Provider agnosticism vs. context reset.** AI providers (Claude Code, Codex) have context limits. Rebuilding task context on every invocation is the supervisor's burden. If context is too large (large codebase, long history), the provider invocation will be expensive or fail. The adapter contract must define context size budgets.

6. **Stack decision blocking.** No code can be written until the stack is chosen. The design phase can proceed, but task planning must flag stack-dependent tasks.

---

## 11. Ready for Proposal

**YES.**

The orchestrator should tell the user:

> Three protocols were read and verified. The headline finding is that **AAP is not an action authorization protocol** — it is a UI interaction capture protocol for browser canvases. The entire action authorization and risk escalation layer must be designed by the framework. AMP and AID are real, substantive drafts with normative text; they can be built against. The server side of both is not open source, so the framework implements its own local provider. The supervisor model is fully compatible with how AMP/AID assign identity. Multi-tenancy is free in the address format if implemented correctly. The stack is undecided and three realistic options are compared. Recommendation: defer AAP for v1 (replace with CLI prompt), defer the full AID server (use a minimal dev server), defer the monitoring web UI (use logs). These three deferrals do not compromise the v1 proof.
