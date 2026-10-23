# AI Solo Startup Framework

[![Built with Gentle-AI](https://raw.githubusercontent.com/Gentleman-Programming/gentle-ai/main/docs/assets/brand/built-with-gentle-ai.png)](https://github.com/Gentleman-Programming/gentle-ai)

A framework for running startups entirely by AI agents. You declare your company as a YAML file — tenant, agents, gateways, and risk policy — and the framework materializes it: each role agent gets a supervised process, an A2A endpoint, and a risk policy. The human interacts only with the CEO agent and approves risk escalations through a minimal monitoring UI.

Supports **pi** (the default), **Claude Code**, and **OpenCode** as provider backends, with per-agent model selection and per-agent system prompts.

Built on the [A2A protocol](https://a2a-protocol.org/latest/) (Linux Foundation).

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
    # provider omitted → pi (the default)
    model: openai-codex/gpt-6-luna
    system_prompt: agents/ceo.md   # optional: per-agent persona file

  - name: engineer
    role: engineer
    provider: claude-code
    model: sonnet

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
| [pi](https://pi.dev) (default) | `pi` | `pi` (or omit `provider`) |
| [Claude Code](https://docs.anthropic.com/en/docs/claude-code) | `claude` | `claude-code` |
| [OpenCode](https://opencode.ai) | `opencode` | `opencode` |

#### Round limit

The optional `max_rounds` field (any provider) caps the provider rounds of one task for that
agent. It defaults to `8` and must be an integer from `1` to `50`; anything else fails startup
with an error naming the agent and the field. When a delegating task reaches the cap it fails
with `supervisor: provider round limit (N) exhausted after delegation`. The agent is not told
its budget automatically, so the system prompt should state it (and reserve the last round for
the final action).

```yaml
agents:
  - name: ceo
    role: ceo
    max_rounds: 12
```

#### Model selection

The `model` field is optional. When set, the adapter passes `--model <model>` to the CLI.

- **pi**: `provider/id` or a bare `id`, optionally with a `:<thinking>` suffix (e.g. `openai-codex/gpt-6-luna`, `sonnet:high`). The model is checked at startup against `pi --list-models` (with the agent's `extensions` loaded), so a typo fails materialize instead of every task.
- **Claude Code**: accepts aliases (`sonnet`, `opus`) or full names (`anthropic/claude-sonnet-4-20250514`)
- **OpenCode**: requires `provider/model` format (e.g. `openai/gpt-4o`, `opencode/mimo-v2.5-free`)

When omitted, each CLI uses its configured default model.

#### Agent isolation

All agents run in **isolated mode** by default, independent of the user's local environment:

| Provider | Isolation flags |
|----------|----------------|
| pi | `-ne --no-session --no-skills --no-prompt-templates --no-context-files --no-approve --offline` — always applied; with MCP also `-e builtin:mcp` + the adapter's own extension and `--tools read,bash,edit,write,mcp__framework__<kind>,...` (exact tool names, never a prefix pattern) |
| Claude Code | `--no-session-persistence --setting-sources "" --disable-slash-commands` — always applied |
| OpenCode | `--pure` — always applied |

These flags are unconditional — there is no opt-out. Isolation is a security baseline, not a feature.

`--safe-mode` is deliberately not used for Claude Code: it disables MCP servers along with
everything else, which would make the framework's own MCP wiring permanently unreachable. The
flags above are the closest substitute that keeps MCP working, but they do not fully replicate
`--safe-mode`'s isolation — CLAUDE.md auto-discovery, plugins, custom commands/agents, and output
styles/workflows/themes/keybindings still load normally, because no flag in the installed CLI
disables them while leaving MCP reachable. See `AGENTS.md` for the full rationale.

For pi, `-ne` disables every discovered, configured, and built-in extension — including
providers that come from extensions (such as `pi-claude-bridge`). Add the ones an agent needs
with the pi-only `extensions` allowlist:

```yaml
agents:
  - name: ceo
    role: ceo
    model: claude-bridge/claude-sonnet-4-6
    extensions:
      - /home/me/.pi/agent/npm/node_modules/pi-claude-bridge   # installed package dir; `~` is not expanded
      - builtin:llama.cpp
```

- Each entry is a file or directory path (relative paths resolve against `company.yaml`; it must
  exist at startup) or a `builtin:<name>` reference.
- Package sources (`npm:`, `git:`, URLs) are rejected at load: pi silently ignores them on `-e`.
- `extensions` is rejected for any provider other than `pi`.
- When the framework's MCP server is wired, `--tools` hides every tool except the four built-ins
  and the framework's own MCP tools, so tools registered by these extensions are not callable;
  use them for providers, not for extra tools. Ambient MCP servers from `~/.pi/agent/mcp.json`
  still connect but their tools are hidden for the same reason.

To give a pi agent its own tools (e.g. a browser for a QA agent), declare stdio MCP servers with
the pi-only `mcp_servers` field:

```yaml
agents:
  - name: qa
    role: qa
    mcp_servers:
      chrome-devtools:
        command: npx
        args: ["chrome-devtools-mcp", "--headless", "--isolated"]
```

- Server names must match `^[a-z0-9][a-z0-9_-]*$`, cannot be `framework`, and, after mapping
  `-` to `_`, must not contain `__` or end with `_`; two names that differ only in `-` and `_`
  are rejected (pi treats them as the same server); `command` is
  required; unknown keys are rejected. `mcp_servers` is rejected for any provider other than `pi`.
- The `--tools` allowlist names MCP tools **exactly**, never by `mcp__<name>__*` prefix, so an
  undeclared ambient MCP server (any name, e.g. `chrome_devtools__prod`) exposes no callable
  tool. The framework server contributes `mcp__framework__<kind>` for every `risk_policy`
  action kind plus `finish_task`. Each declared server is **probed once at startup**
  (materialize): the framework spawns it, runs the MCP `initialize` handshake, lists its tools
  (following pagination) within a 30-second bound, and closes it. Its tools are allowlisted as
  `mcp__<name>__<tool>`, with every character outside `[A-Za-z0-9_]` in either part replaced by
  `_` (pi's naming), so `chrome-devtools`'s `take_snapshot` is `mcp__chrome_devtools__take_snapshot`.
  Materialize aborts, before any server binds, when a probe fails or times out, when a server
  exposes zero tools, when two of its tools normalize to the same name (pi would hash-suffix
  them), or when a normalized tool name (or a `risk_policy` action kind) contains `__` or starts
  with `_`; each error names the server or kind. These name rules keep `mcp__<server>__<tool>`
  parseable one way, so no other server/tool pair, declared or ambient, can produce an
  allowlisted name. Tools a server adds **after** startup are not callable
  (fail closed): restart the company to pick them up. `command` and `args` are written to a
  temporary extension file, so do not put secrets in them.
- Residual: an ambient `~/.pi/agent/mcp.json` server with exactly the same name as a declared
  server (or `framework`) replaces it in pi, because pi gives `mcp.json` precedence over
  registered servers. Do not reuse those names in your own pi config.
- **These tools are not action intents and bypass the risk policy.** The framework neither
  gates nor audits their calls; declaring a server is an explicit grant by the company owner.

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
- **pi**: the file is passed via `--append-system-prompt <absolute-path>` (appended to pi's own system prompt).
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

The monitoring UI starts at `http://127.0.0.1:8080`. Override with:

```bash
export COMPANY_UI_ADDR="0.0.0.0:9090"
```

### 5. Interact

Open **`http://127.0.0.1:8080`** — this is the primary way to interact with your company. The UI lets you:

- Send tasks to the CEO agent
- Monitor task state in real time; tasks an agent delegates to other agents appear nested under the delegating task
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
│  └─ Policy engine  │  └─ Policy engine  │
├─────────────────────────────────────────┤
│  Shared: policy engine, gateway         │
└─────────────────────────────────────────┘
```

## Key Concepts

- **Company as code**: Your company is a YAML file, reviewable in a PR, version-controlled
- **Agent isolation**: Agents always run with isolation flags (`-ne` plus no skills/templates/context files/session for pi, `--no-session-persistence --setting-sources "" --disable-slash-commands` for Claude Code, `--pure` for OpenCode), independent of local config — see [Agent isolation](#agent-isolation) for the accepted tradeoff
- **System prompts**: Per-agent persona files enforce declared roles; loaded once at startup
- **Risk policy**: Actions are classified as `safe`, `risky`, or `hard-deny` based on role
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
