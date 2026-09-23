# Project
A framework for running startups entirely by AI agents. You declare your company as a YAML file — tenant, agents, gateways, and risk policy — and the framework materializes it: each role agent gets a supervised process, an A2A endpoint, and a risk policy. The human interacts only with the CEO agent and approves risk escalations through a minimal monitoring UI.

- GO language it's used to build the framework.
- YAML to define a new company.
- Markdown to define the skills of each agent inside of a company
- A2A protocol to build the communication between agents (Linux Foundation)
- We follow the SDD flow for every feature made, and storing the specs in openspec and engram (if available)

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
(verified against the installed 2.1.268 CLI), `--safe-mode` disables "CLAUDE.md, skills,
plugins, hooks, MCP servers, custom commands and agents, output styles, workflows, custom
themes, keybindings" as one bundle — MCP servers are explicitly included, so combining
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


