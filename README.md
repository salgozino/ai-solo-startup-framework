# AI Solo Startup Framework

[![Built with Gentle-AI](https://raw.githubusercontent.com/Gentleman-Programming/gentle-ai/main/docs/assets/brand/built-with-gentle-ai.png)](https://github.com/Gentleman-Programming/gentle-ai)

A framework for running startups entirely by AI agents. You declare your company as a YAML file — tenant, agents, gateways, and risk policy — and the framework materializes it: each role agent gets a supervised task queue, an A2A endpoint, and a risk policy. The human interacts only with the CEO agent and approves risk escalations through a minimal monitoring UI.

Supports **Claude Code** and **OpenCode** as provider backends, with per-agent model selection and per-agent system prompts.

Built on the [A2A protocol](https://a2a-protocol.org/latest/) (Linux Foundation).

## What works today

| Capability | What you actually get |
|------------|----------------------|
| **Company as one file** | `company.yaml` declares tenant, agents, providers, models, personas, gateways, and risk policy. Unknown keys and inline secrets are rejected at load time, before anything starts. |
| **Per-agent runtime** | Each agent gets its own loopback A2A endpoint, its own task queue, and its own persisted task store. |
| **Two providers** | Claude Code and OpenCode, with per-agent model selection and per-agent system prompt files. |
| **Isolation by default** | Isolation flags are always applied, with no opt-out — see [Agent isolation](#agent-isolation) for the one accepted gap. |
| **Risk policy, fail-closed** | Every action an agent wants to take is classified before it runs. An action kind that is not declared in `risk_policy` is **denied**, never allowed by default. |
| **Human approval** | A risky action pauses the task at `INPUT_REQUIRED` and waits for you to approve or reject in the UI. Nothing executes until you decide. |
| **Crash recovery** | Open tasks survive a restart: in-flight work is re-submitted and pending approvals stay approvable. |
| **One-hop delegation** | The CEO can hand a task to a peer agent (e.g. the engineer) over A2A and block for the result. |
| **Telegram (outbound)** | An approved action sends *you* a Telegram message. The bot never listens for inbound messages. |
| **Multi-tenancy** | One tenant per process, enforced at the transport edge. Requests for a foreign tenant are rejected before any work runs. |
| **Authenticated A2A** | Every inbound A2A request requires a Bearer token, checked with a constant-time compare, before the tenant check. |

## What is NOT supported yet

Read this before you design around the framework. These are known gaps, not bugs.

| Gap | What it means in practice |
|-----|---------------------------|
| **No conversation between agents** | Every task is **single-shot**. The CEO delegates once, receives one answer, and the peer's subprocess has already exited. There is no second turn to send feedback into — so the **CEO cannot loop with the engineer to refine a plan**. To iterate, *you* send a new task. |
| **No memory across tasks** | An agent receives the raw task text and nothing else. There is no conversation history, no prior-task context, and no shared state between runs. |
| **Chained approval is not wired** | If a delegated peer hits a risky action and escalates, the *delegating* task fails immediately. Only the agent you talk to directly can hold an approval. |
| **The UI only shows the first agent** | The monitoring UI is bound to the CEO. A peer agent's tasks are invisible and its escalations are unapprovable. This is the prerequisite for chained approval. |
| **The UI has no authentication** | `POST /api/send`, `/api/approve`, and `/api/reject` are open to anyone who can reach the port. Keep it on loopback. |
| **Only `risky` is honored** | In `risk_policy`, the `risk` field is only compared against `"risky"`. Any other value — including a typo — falls through and the action **executes silently**. Denial today comes from `allowed_roles` or from an undeclared action kind, not from a `risk` level. |
| **No process supervision** | "Supervisor" means a task-lifecycle state machine, not a daemon. There is no long-running agent process, no health probe, no restart-on-crash. Provider CLIs are ephemeral: one subprocess per task. |
| **Telegram is the only gateway** | `email` is accepted by the channel allow-list but no email gateway exists. Every non-delegation action is routed to Telegram. |
| **Streaming is advertised, not used** | The Agent Card declares `streaming: true`, but the framework's own client never opens a stream. |
| **No live-CLI end-to-end coverage** | The full two-agent delegation flow is exercised against test doubles, not against real `claude` / `opencode` binaries in CI. |

## Quickstart

### 1. Install

```bash
git clone https://github.com/salgozino/ai-solo-startup-framework.git
cd ai-solo-startup-framework
go build ./cmd/company
```

### 2. Create your company

Create a `company.yaml`:

```yaml
tenant: acme
auth_token_env: COMPANY_A2A_TOKEN   # required: Bearer token env var for the A2A transport

agents:
  - name: ceo
    role: ceo
    provider: claude-code
    model: anthropic/claude-sonnet-4-20250514
    system_prompt: agents/ceo.md   # optional: per-agent persona file

  - name: engineer
    role: engineer
    provider: opencode
    model: openai/gpt-4o

gateways:
  telegram:
    token_env: TELEGRAM_BOT_TOKEN
    recipient_env: TELEGRAM_OWNER_ID

risk_policy:
  telegram_send:
    risk: risky
    allowed_roles:
      - ceo
```

#### Supported providers

| Provider | CLI binary | `provider` value |
|----------|-----------|-----------------|
| [Claude Code](https://docs.anthropic.com/en/docs/claude-code) | `claude` | `claude-code` |
| [OpenCode](https://opencode.ai) | `opencode` | `opencode` |

#### Model selection

The `model` field is optional. When set, the adapter passes `--model <model>` to the CLI.

- **Claude Code**: accepts aliases (`sonnet`, `opus`) or full names (`anthropic/claude-sonnet-4-20250514`)
- **OpenCode**: requires `provider/model` format (e.g. `openai/gpt-4o`, `opencode/mimo-v2.5-free`)

When omitted, each CLI uses its configured default model.

#### Agent isolation

All agents run in **isolated mode** by default, independent of the user's local environment:

| Provider | Isolation flags |
|----------|----------------|
| Claude Code | `--no-session-persistence --setting-sources "" --disable-slash-commands` — always applied |
| OpenCode | `--pure` — always applied |

These flags are unconditional — there is no opt-out. Isolation is a security baseline, not a feature.

`--safe-mode` is deliberately not used for Claude Code: it disables MCP servers along with
everything else, which would make the framework's own MCP wiring permanently unreachable. The
flags above are the closest substitute that keeps MCP working, but they do not fully replicate
`--safe-mode`'s isolation — CLAUDE.md auto-discovery, plugins, custom commands/agents, and output
styles/workflows/themes/keybindings still load normally, because no flag in the installed CLI
disables them while leaving MCP reachable. See `AGENTS.md` for the full rationale.

#### System prompts and the `agents/` folder

The optional `system_prompt` field declares a per-agent persona file:

```yaml
agents:
  - name: ceo
    role: ceo
    provider: claude-code
    system_prompt: agents/ceo.md   # relative to company.yaml
```

- The path is resolved relative to the directory of `company.yaml`.
- The file must exist at startup; the framework fails fast with a descriptive error if it is missing.
- **Claude Code**: the file is passed via `--system-prompt-file <absolute-path>`.
- **OpenCode**: the file content is read once at startup and prepended to every task as `[SYSTEM]\n{content}\n\n`.

Convention: keep all agent persona files in an `agents/` folder next to `company.yaml`:

```
company.yaml
agents/
  ceo.md
  engineer.md
```

When `system_prompt` is absent, agents start normally with only the isolation flags applied.

### 3. Set environment variables

```bash
# COMPANY_A2A_TOKEN authenticates every A2A request (Bearer auth) — required
export COMPANY_A2A_TOKEN="your-a2a-bearer-token"
export TELEGRAM_BOT_TOKEN="your-bot-token"
export TELEGRAM_OWNER_ID="your-telegram-user-id"
```

### 4. Run

```bash
./company materialize company.yaml
```

The monitoring UI starts at `http://127.0.0.1:8080`. Override the port with:

```bash
export COMPANY_UI_ADDR="127.0.0.1:9090"
```

> **Warning:** the UI has **no authentication**. Anyone who can reach the port can send tasks
> and approve risky actions. Keep it bound to loopback — do not bind it to `0.0.0.0`.

### 5. Interact

Open **`http://127.0.0.1:8080`** — this is the primary way to interact with your company. The UI lets you:

- Send tasks to the CEO agent
- Monitor task state in real time
- Approve or reject risky actions (e.g. sending a Telegram message)

**What happens when you send a task:**

1. CEO receives the task via A2A
2. If the task involves a risky action (like `telegram_send`), it escalates to `INPUT_REQUIRED`
3. The task appears in the UI with Approve/Reject buttons
4. Approve → the action executes via Telegram (outbound message to you)
5. Reject → the task is rejected, nothing is sent

> **Note:** The Telegram gateway is outbound only — you don't send messages to the bot,
> the bot sends messages to you after you approve the action in the UI.

#### Advanced: direct A2A endpoint

The output also shows each agent's A2A endpoint URL:

```
wire: agent "ceo" started at http://127.0.0.1:54321 (role=ceo)
wire: agent "engineer" started at http://127.0.0.1:54322 (role=engineer)
```

You can send tasks directly via the A2A `/invoke` endpoint (useful for scripting or integration):

```bash
curl -X POST http://127.0.0.1:54321/invoke \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "SendMessage",
    "params": {
      "tenant": "acme",
      "message": {
        "messageId": "msg-001",
        "role": "user",
        "parts": [{"text": "Send a telegram saying hello"}]
      }
    },
    "id": "1"
  }'
```

Replace `54321` with the port from your output.

## Architecture

```
company.yaml
    │
    ▼
┌─────────────────────────────────────────┐
│  company materialize                    │
│  (composition root — wire.go)           │
├─────────────────────────────────────────┤
│  CEO Supervisor    │  Engineer Supervisor│
│  ├─ A2A endpoint   │  ├─ A2A endpoint   │
│  ├─ Task queue     │  ├─ Task queue     │
│  └─ Task store     │  └─ Task store     │
├─────────────────────────────────────────┤
│  Shared: policy engine, MCP server,     │
│          gateway, peer directory        │
└─────────────────────────────────────────┘
```

## Key Concepts

- **Company as code**: Your company is a YAML file, reviewable in a PR, version-controlled
- **Agent isolation**: Agents always run with isolation flags (`--no-session-persistence --setting-sources "" --disable-slash-commands` for Claude Code, `--pure` for OpenCode), independent of local config — see [Agent isolation](#agent-isolation) for the accepted tradeoff
- **System prompts**: Per-agent persona files enforce declared roles; loaded once at startup
- **Risk policy**: Every action is classified before it runs — permitted, escalated to you, or denied. Undeclared action kinds are denied, not permitted (see [What is NOT supported yet](#what-is-not-supported-yet) for the current `risk` field limitation)
- **Human-in-the-loop**: Risky actions escalate to the monitoring UI for approval
- **Multi-tenancy**: Multiple companies run on the same machine without interference
- **A2A protocol**: Agents communicate via standard A2A endpoints on loopback

## Development

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Build
go build ./cmd/company
```

## License

MIT — see [LICENSE](LICENSE) for details.
