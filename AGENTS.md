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
  MCP config shape: opencode's own schema (https://opencode.ai/config.json), NOT Claude's
  `mcpServers` shape — the root `Config` type declares `additionalProperties: false`, so an
  unrecognized top-level key such as `mcpServers` invalidates the whole config and the server
  is silently never registered. The adapter emits `{"mcp": {"framework": {"type": "remote",
  "url": ..., "enabled": true, "headers": {"Authorization": "Bearer <token>"}}}}`.
- **claude**: the adapter requires `--mcp-config`, `--strict-mcp-config`, and
  `--output-format stream-json`. It never requests `--json-schema`. Its `--mcp-config` file
  uses the Claude-shaped `mcpServers` envelope, which is unrelated to opencode's schema above.

Both adapters receive MCP configuration ephemerally per invocation — claude through a `0600`
temp file removed on exit, opencode through the subprocess-scoped `OPENCODE_CONFIG_CONTENT`
environment variable. Neither adapter reads or writes a persisted user config.


