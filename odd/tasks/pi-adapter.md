# Pi provider adapter

Add a `pi` provider adapter (pi coding agent CLI, verified on pi 1.2.0) next to `claude-code` and
`opencode`, and make `pi` the default provider. Branch: `feat/pi-adapter`.

## Specs

- S1 — "Quiero qeu creemos un neuvo adapter, para que todo el sistema funcione con pi (ademas de claude, etc)":
  a new `adapters/pi` package implements `port.Provider`; `provider: pi` in company.yaml wires it in
  `cmd/company/wire.go`; claude-code and opencode keep working unchanged.
- S2 — "pi va a ser el sistema por default": an agent that omits `provider` gets `pi`
  ("Omitido → pi + ejemplos"); company.yaml and README examples use `pi`.
- S3 — Isolation ("Aislado + allowlist en YAML"): every invocation runs `-ne` (no discovered,
  configured or built-in extensions), no skills, no prompt templates, no context files, no
  project-local files, no session persistence, offline startup. When MCP is wired, only
  `builtin:mcp` plus the adapter's own ephemeral extension load, and `--tools` restricts the
  agent to `read,bash,edit,write,mcp__framework__*` so ambient user MCP servers cannot run
  actions outside the risk policy.
- S4 — Allowlist in YAML: new optional per-agent field `extensions:` (list). Each entry is a
  filesystem path (resolved relative to company.yaml, must exist) or `builtin:<name>`; each is
  passed as `-e <entry>`. `npm:` / other package sources are rejected at load with a named error
  (pi silently ignores `-e npm:...`). The field is rejected for non-pi providers.
- S5 — MCP delivery is ephemeral and per invocation: the bearer token and URL reach pi only via
  subprocess-scoped env vars read by a 0600 temp extension file that is removed on exit; the
  token never appears on argv and no persisted pi config is read or written for it.
- S6 — Input on stdin (no argv ceiling); system prompt via `--append-system-prompt <path>`.
- S7 — Failure detection: pi exits 0 even when the model call fails. A final assistant
  `message_end` with `stopReason` `error` or `aborted` is a RunTask error carrying `errorMessage`.
  Non-zero exit, deadline, never-contacted MCP endpoint and MCP health errors fail like siblings.
- S8 — Output = text blocks of assistant `message_end` messages, in order; never tool calls;
  ActionIntents come only from the MCP sink. Non-event output falls back to raw text; truncation
  marker on cap.
- S9 — `ProbeModel`: at startup, `pi -ne [extensions] --offline --list-models` must list the
  configured model (`provider/id`, `id`, optional `:thinking` suffix), else materialize fails
  naming the model; an unrunnable binary fails too.

## Tasks

- T1 [S1,S3,S5,S6,S7,S8] adapters/pi RunTask + fakepi + tests — inline — commit: pending
- T2 [S9] ProbeModel — inline — commit: pending
- T3 [S2,S4] config: `extensions` field, default provider `pi` — inline — commit: 9174d48 (done)
- T4 [S1,S2,S4] wire.go `pi` case — inline — commit: pending
- T5 [S1-S9] docs: AGENTS.md, README, company.yaml, openspec change — inline — commit: pending
- T6 verify (security: token/tool isolation) — gentle-ai-verify — pending

## Log

- L1 (user): "Quiero qeu creemos un neuvo adapter, para que todo el sistema funcione con pi (ademas de claude, etc), pero pi va a ser el sistema por default pq por lo que vi es lo que mejor va a funcionar con esto"
- L2 (user decisions): isolation = "Aislado + allowlist en YAML"; default = "Omitido → pi + ejemplos (Recomendado)".
- L3 (evidence, pi 1.2.0 spike against transport/mcp): `-ne -e builtin:mcp -e ext.ts` with
  `pi.registerMcpServer("framework",{url,headers,exposure:"direct"})` reaches the server
  (contacted=true) and records telegram_send + finish_task intents. Prompt via stdin works.
  Exit code is 0 with `stopReason:"error"` on model failure. `-e npm:pkg` is silently ignored;
  a directory path works. Ambient `~/.pi/agent/mcp.json` servers still load under `-ne`;
  `--tools read,bash,edit,write,mcp__framework__*` hides them.
- L4 (evidence): T1/T2 by gentle-ai-worker (adapters/pi, package tests green). `pi --list-models`
  writes the table to stdout (real binary). Real E2E with adapters/pi + transport/mcp + pi 1.2.0:
  gpt-6-luna → Output "DONE", intents telegram_send + finish_task; probe rejects unlisted
  `openai-codex/gpt-5.4-mini`; RunTask on it returns "pi: model call failed: Codex error ..." (exit 0 case).
  `--append-system-prompt <path>` injects file contents (verified). T4 wire RED ("unknown provider
  \"pi\"") → GREEN. company.yaml is gitignored (local); switched to pi per L2.
