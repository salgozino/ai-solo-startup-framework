# AI Solo Startup Framework

A framework for running startups entirely by AI agents. You declare your company as a YAML file — tenant, agents, gateways, and risk policy — and the framework materializes it: each role agent gets a supervised process, an A2A endpoint, and a risk policy. The human interacts only with the CEO agent and approves risk escalations through a minimal monitoring UI.

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

agents:
  - name: ceo
    role: ceo
    provider: claude-code
  - name: engineer
    role: engineer
    provider: claude-code

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

### 3. Set environment variables

```bash
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

The output shows each agent's A2A endpoint:

```
wire: agent "ceo" started at http://127.0.0.1:54321 (role=ceo)
wire: agent "engineer" started at http://127.0.0.1:54322 (role=engineer)
```

Send a task to the CEO via its A2A endpoint (`/invoke`):

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

**What happens next:**

1. CEO receives the task via A2A
2. If the task involves a risky action (like `telegram_send`), it escalates to `INPUT_REQUIRED`
3. Open `http://127.0.0.1:8080` — you'll see the task with Approve/Reject buttons
4. Approve → the action executes via Telegram (outbound message to you)
5. Reject → the task is rejected, nothing is sent

> **Note:** The Telegram gateway is outbound only — you don't send messages to the bot,
> the bot sends messages to you after you approve the action in the UI.

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
