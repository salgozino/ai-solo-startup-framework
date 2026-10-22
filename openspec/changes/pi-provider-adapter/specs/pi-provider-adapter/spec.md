# pi Provider Adapter Specification

## Purpose

Run agents on the pi coding agent CLI (verified on pi 1.2.0) with the same guarantees as the
claude and opencode adapters: isolated, ephemeral per-invocation MCP wiring, intents from the
MCP sink only, and loud failures instead of false successes.

## Requirements

### Requirement: pi Is the Default Provider

An agent entry that omits `provider` MUST load with provider `pi`. Explicit providers MUST be kept.

#### Scenario: omitted provider
- GIVEN an agent entry without `provider`
- WHEN company.yaml is loaded
- THEN the agent's provider is `pi`

### Requirement: Extensions Allowlist

A pi agent MAY declare `extensions`, a list of file/directory paths (resolved against
company.yaml, must exist) or `builtin:<name>` references, each passed as `-e <entry>`.
Package sources (`npm:`, `git:`, URLs) and use with a non-pi provider MUST fail load.

#### Scenario: npm source rejected
- GIVEN `extensions: [npm:pi-claude-bridge]`
- WHEN company.yaml is loaded
- THEN load fails naming `extensions[0]` and that package sources are not supported

### Requirement: Isolated Invocation

Every invocation MUST pass `-p --mode json --no-session -ne --no-skills --no-prompt-templates
--no-context-files --no-approve --offline` and deliver the input on stdin, never argv.

### Requirement: Ephemeral MCP via Temp Extension

When MCP is wired, the adapter MUST load `builtin:mcp` and a 0600 temp extension that registers
the `framework` server with `direct` exposure, reading URL and token from subprocess-scoped env
vars; the token MUST NOT appear on argv or in the file, and the file MUST be removed on exit.
`--tools read,bash,edit,write,mcp__framework__*` MUST restrict callable tools.

#### Scenario: never contacted
- GIVEN MCP is wired and the CLI never presents the minted token
- WHEN the process exits with no intents
- THEN RunTask fails instead of reporting success

### Requirement: Model Failure Is a Task Failure

Because pi exits 0 on model failure, a final assistant `message_end` with `stopReason` `error`
or `aborted` MUST make RunTask return an error carrying `errorMessage`.

#### Scenario: unsupported model at runtime
- GIVEN pi emits a final assistant message with `stopReason: "error"` and exits 0
- WHEN RunTask parses the stream
- THEN it returns an error containing the `errorMessage`

### Requirement: Startup Model Probe

`ProbeModel` MUST fail materialize when the configured model is not listed by
`pi -ne [-e <extensions>] --offline --list-models`, accepting `provider/id`, `id` and a
`:<thinking>` suffix.
