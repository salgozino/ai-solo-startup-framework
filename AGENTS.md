# Project
A framework for running startups entirely by AI agents. You declare your company as a YAML file — tenant, agents, gateways, and risk policy — and the framework materializes it: each role agent gets a supervised process, an A2A endpoint, and a risk policy. The human interacts only with the CEO agent and approves risk escalations through a minimal monitoring UI.

- GO language it's used to build the framework.
- YAML to define a new company.
- Markdown to define the skills of each agent inside of a company
- A2A protocol to build the communication between agents (Linux Foundation)
- We follow ODD (Organic Driven Development): every feature is tracked in `odd/tasks/<feature-name>.md`
  (objective, scope, constraints, tasks, progress log), mirrored to Engram under `odd/<feature-name>/tasks`.
  TDD stays mandatory; each task closes with a work-unit Conventional Commit.
  `openspec/` is a frozen, read-only historical record of the pre-2026-10-02 SDD era,
  consulted as design reference only.

# How to build
go build ./cmd/company

# How to test
TDD is mandatory for this project, then you need to run the tests before pushing a PR
```
go test ./...
```

It's very important to run all the tests before pushing a PR. There is a CI that will block the PRs, so we should validate before pushing.

# Provider CLI compatibility

The provider adapters drive external agent CLIs as subprocesses and parse their structured output.

- **pi** (default provider — an agent that omits `provider` gets `pi`): verified against
  `pi 1.2.0`. The adapter runs `pi -p --mode json --no-session -ne --no-skills
  --no-prompt-templates --no-context-files --no-approve --offline`, writes the task input to
  **stdin** (no argv ceiling, like claude), and passes `system_prompt` as
  `--append-system-prompt <path>` (pi appends the file's contents to its own system prompt).
  Exit code is NOT a success signal: pi exits 0 when the model call fails and reports it as a
  final assistant `message_end` with `stopReason: "error"` (or `"aborted"`) and an
  `errorMessage`; the adapter turns that into a RunTask error. Output is the `text` blocks of
  assistant `message_end` messages, in order.
  MCP delivery: pi reads MCP servers only from persisted `mcp.json` files, so the adapter
  instead writes a static 0600 temp extension (`-e <file>.ts`, removed on exit) that calls
  `pi.registerMcpServer("framework", {url, headers, exposure: "direct"})`, reading the URL and
  bearer token from the subprocess-scoped `ASF_MCP_URL`/`ASF_MCP_TOKEN` env vars; the token is
  never on argv or in the file. `-e builtin:mcp` re-enables the built-in MCP client that `-ne`
  disables.
  Isolation: `-ne` disables every discovered, configured and built-in extension, including
  providers that come from extensions (e.g. `pi-claude-bridge`). Ambient servers from
  `~/.pi/agent/mcp.json` still connect under `-ne`, and a `direct`-exposed one would let the
  agent act outside the risk policy, so with MCP wired the adapter passes
  `--tools read,bash,edit,write,mcp__framework__<kind>,...` (exact names, see below; the counterpart of claude's
  `--strict-mcp-config`). Accepted tradeoff: tools registered by allowlisted extensions are
  hidden by that same allowlist.
  Per-agent `extensions:` (pi only) adds `-e <entry>` for each entry: a file/directory path
  (resolved against company.yaml, must exist) or `builtin:<name>`. Package sources (`npm:`,
  `git:`, URLs) are rejected at load because `pi -e npm:<pkg>` is silently ignored (verified).
  Per-agent `mcp_servers:` (pi only; name -> `{command, args}`, name `^[a-z0-9][a-z0-9_-]*$`
  and not `framework`; names equal after `-`->`_` collide in pi and are rejected; after that
  mapping no `__` and no trailing `_`) registers each
  stdio server through `pi.registerMcpServer(name, {command, args, exposure: "direct"})` in the
  same temp extension (verified against pi 1.2.0 `docs/extensions.md`: stdio `command`/`args` are
  accepted). The `--tools` allowlist names MCP tools EXACTLY, never `mcp__<server>__*`: a prefix
  would also match an undeclared ambient server (`<name>__x` -> `mcp__<name>__x__<tool>`), and
  `--exclude-tools` is not used. pi names tools `mcp__<server>__<tool>` replacing every
  non-`[A-Za-z0-9_]` character with `_` (`docs/mcp.md`; `toolName` in
  `adapters/pi/mcp_probe.go`). Framework: `mcp__framework__<kind>` for each
  `Options.PolicyActionKinds` entry plus `finish_task` (the exact set `transport/mcp`
  `registerTools` registers), sorted. Declared servers: `Adapter.ProbeMCP`, called from the
  `mcpProber` loop in `cmd/company/wire.go` right after `ProbeModel` (so a failure aborts
  materialize before any server binds), spawns each server once over stdio with the MCP Go SDK
  (`mcp.CommandTransport` + `Client.Connect` + `ClientSession.Tools`, which follows
  `nextCursor`), bounded by `Options.MCPProbeTimeout` (30s default) per server, then closes it.
  Named failures: probe failed/timed out, zero tools, two tools of one server normalizing to the
  same name (pi would hash-suffix them), and an ambiguous name: a normalized server with `__` or a
  trailing `_`, or a normalized tool / policy kind with `__` or a leading `_`. Those rules make the
  separator the only `__` in `mcp__<server>__<tool>`, so the flat name parses one way and no other
  server/tool pair (declared or ambient) can produce it (native review R1-ambiguous-tool-ownership,
  R3-cross-server-tool-collision). `RunTask` fails closed if declared servers were never
  probed. Tools a server adds after startup are not callable. Residual: an ambient `mcp.json` server with exactly the
  same name as a declared/framework server replaces it in pi (mcp.json wins over registered
  servers). Without `mcp_servers` the extension is byte-identical and only the framework part of
  `--tools` changed (prefix -> exact names). These tools
  are NOT action intents and bypass the risk policy: declaring a server is an explicit grant by
  the company owner. Command/args live in the extension file, never put secrets there.
  `ProbeModel` checks the configured model against `pi -ne [-e ...] --offline --list-models`.

- **opencode**: the adapter requires `--format json` NDJSON output support. Verified locally with
  `opencode 1.18.31`; no upstream confirmation of the exact floor is available as of this change.
  Older versions that do not emit NDJSON on `--format json` will yield empty parsed output.
  `Adapter.ProbeModel` (`adapters/opencode/adapter.go`) checks this at startup — via the
  `modelProber` structural probe loop in `cmd/company/wire.go` — and fails materialize loudly,
  naming the flag and this version floor, instead of letting the flag fail silently on every
  `RunTask` call at runtime.
  NDJSON event shape: every event shares one envelope — `type`, `timestamp`, `sessionID`,
  and a nested `part` object — and the assistant's text lives at **`part.text`**, never at
  the top level. Verified against `opencode 1.18.31`: the complete top-level field set is
  exactly `['part','sessionID','timestamp','type']`. `parseStreamText` reads `part.text`
  from events whose top-level `type` is `"text"`. Its raw-stdout fallback is gated on
  "this was not an event stream at all", NOT on "extraction was empty" — a well-formed
  stream carrying only lifecycle or `error` events must yield empty output, because
  dumping the envelope would return the raw NDJSON as the agent's answer.
  `testdata/fakeopencode` emits this full envelope on purpose: it previously emitted a
  simplified `{"type":"text","text":...}` shape that does not exist, which kept the suite
  green while the adapter could not parse a single real response.
  MCP config shape: opencode's own schema (https://opencode.ai/config.json), NOT Claude's
  `mcpServers` shape — the root `Config` type declares `additionalProperties: false`, so an
  unrecognized top-level key such as `mcpServers` invalidates the whole config and the server
  is silently never registered. The adapter emits `{"mcp": {"framework": {"type": "remote",
  "url": ..., "enabled": true, "headers": {"Authorization": "Bearer <token>"}}}}`.
  Input size limit: the opencode adapter passes the task input as a positional **argv**
  argument, so a task whose input grows past the Linux per-argument ceiling
  (`MAX_ARG_STRLEN`, 131072 bytes) fails at `execve` with `E2BIG`. opencode hits this
  sooner than claude because the system prompt is concatenated into that same argv string.
  This is unsupported by decision, not by oversight: there is no preflight size check and
  no stdin fallback. Use the claude adapter for any agent whose input can grow (a
  multi-round transcript, a long document).
- **claude**: the adapter requires `--mcp-config`, `--strict-mcp-config`, and
  `--output-format stream-json`. It never requests `--json-schema`. Its `--mcp-config` file
  uses the Claude-shaped `mcpServers` envelope, which is unrelated to opencode's schema above.
  Input delivery: the task input is written to the child's **stdin**, never argv — `claude -p`
  reads the prompt from stdin when no positional prompt argument is given. stdin imposes no
  per-argument ceiling at all, while argv caps a single argument at 131072 bytes, so a growing
  input (a multi-round transcript) stays deliverable here but not on argv (see opencode above).

Both adapters receive MCP configuration ephemerally per invocation — claude through a `0600`
temp file removed on exit, opencode through the subprocess-scoped `OPENCODE_CONFIG_CONTENT`
environment variable. Neither adapter reads or writes a persisted user config.

## claude adapter isolation flags (decision item)

The claude adapter deliberately does **not** pass `--safe-mode`. Per `claude --help`
(re-verified against the installed 2.1.280 CLI), `--safe-mode` disables "CLAUDE.md, skills,
installed plugins, hooks, MCP servers, custom commands and agents, output styles, workflows,
custom themes, keybindings, and more" as one bundle — MCP servers are still explicitly
included on 2.1.280, so the decision still holds for its original reason: combining
`--safe-mode` with `--mcp-config`/`--strict-mcp-config` made the MCP endpoint permanently
unreachable and every MCP-wired `RunTask` invocation fail.

No flag combination in the installed CLI replicates `--safe-mode`'s full isolation while
leaving MCP enabled:
- `--bare` disables a similar bundle but also restricts Anthropic auth to
  `ANTHROPIC_API_KEY`/`apiKeyHelper` only (OAuth and keychain are never read), which would
  break the adapter's existing auth story.
- `--restricted` strips Bash/code-execution tools the agents need to do their job.

The adapter instead passes `--setting-sources ""` (skips user/project/local `settings.json`,
where hooks and permission overrides normally live) and `--disable-slash-commands` (skips
the user's own installed skills) as the closest available substitute, alongside
`--strict-mcp-config` (already required above), which independently ensures MCP comes only
from the adapter's own `--mcp-config`, never the user's ambient MCP config.

**Known isolation regression (accepted tradeoff, not an oversight):** CLAUDE.md
auto-discovery, plugins, custom commands/agents, and output styles/workflows/themes/
keybindings are not covered by any known flag and now load normally for every invocation.
This is a decision item for the orchestrator: it may be revisited if a future CLI version
adds a more granular disable flag, or if the composition root should set `cmd.Dir` to a
directory known not to contain a `CLAUDE.md`.


