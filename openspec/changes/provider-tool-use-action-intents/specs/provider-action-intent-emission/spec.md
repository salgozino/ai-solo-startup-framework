# Provider Action Intent Emission Specification

## Purpose

Real adapters (`claudecode`, `opencode`) declare the MCP server's tools natively to their CLI
per invocation and populate `ProviderResult.ActionIntents` from the MCP server's authenticated
sink — never by parsing each CLI's divergent `tool_use` wire format. Stream/output parsing is
reduced to extracting free-form text for `ProviderResult.Output`.

## Requirements

### Requirement: Ephemeral, Per-Invocation MCP Configuration

Each adapter MUST point its CLI subprocess at the running MCP server using ephemeral,
per-invocation configuration that does not mutate persisted user configuration. Claude MUST
use `--mcp-config` with `--strict-mcp-config`. OpenCode MUST use `OPENCODE_CONFIG_CONTENT`.

#### Scenario: Claude adapter configures MCP ephemerally

- GIVEN a task invocation for the claude adapter
- WHEN it spawns the `claude` subprocess
- THEN it passes `--mcp-config <ephemeral-config> --strict-mcp-config` pointing at the running
  MCP server, and the CLI's persisted config file is unmodified after the process exits

#### Scenario: OpenCode adapter configures MCP ephemerally

- GIVEN a task invocation for the opencode adapter
- WHEN it spawns the `opencode` subprocess
- THEN it sets `OPENCODE_CONFIG_CONTENT` for that process only, and the persisted config's
  modification time is unchanged after the process exits

### Requirement: `ActionIntents` Are Collected From the Sink, Never From Stream Parsing

After a subprocess exits, `RunTask` MUST populate `ProviderResult.ActionIntents` from the MCP
server's sink for that invocation's token. It MUST NOT derive `ActionIntent`s by parsing
`tool_use` blocks, `mcp__<server>__<tool>` / `<server>_<tool>` naming, or any CLI event schema.

#### Scenario: A real tool call yields a non-empty ActionIntents

- GIVEN a real `claude` or `opencode` subprocess invokes the declared MCP tool during a task
- WHEN `RunTask` returns
- THEN `ProviderResult.ActionIntents` contains the intent recorded by the server's sink for
  that invocation's token

#### Scenario: No tool call yields an empty, not erroneous, result

- GIVEN a real subprocess completes without invoking any MCP tool
- WHEN `RunTask` returns
- THEN `ProviderResult.ActionIntents` is empty and `err` is nil

### Requirement: Structured Output Parsing Extracts Text Only

Adapters MAY parse structured output streams (claude `--output-format stream-json --verbose`;
opencode `--format json` NDJSON) solely to extract text for `ProviderResult.Output`. Adapters
MUST NOT use claude `--json-schema`: it is a synthetic `StructuredOutput` tool with
`endsTurn: true` that ends the turn and cannot carry free-form `Output` alongside intents.

#### Scenario: Claude adapter does not request `--json-schema`

- GIVEN the claude adapter builds its subprocess argv
- WHEN it assembles flags for structured output
- THEN it does not include `--json-schema`

#### Scenario: OpenCode adapter terminates its reader on process exit, not a sentinel event

- GIVEN the opencode adapter reads NDJSON from `--format json`
- WHEN the subprocess's session reaches `idle` and exits
- THEN the adapter's reader terminates on EOF/process exit, not on any dedicated terminal
  event line

### Requirement: Adapter-Level Tests Exercise the Real Adapter Path, Not Only `fake.Provider`

For each real adapter, the test suite MUST include at least one test that spawns a fake CLI
binary double which performs an actual MCP `tools/call` over HTTP against a running (or
equivalently faithful) MCP server, and asserts `RunTask` returns non-empty `ActionIntents`
populated from that call. Tests that construct `ActionIntents` only via `fake.Provider` MUST
NOT be treated as satisfying this requirement.

#### Scenario: Claude fake-binary test proves the real path is wired

- GIVEN a fake `claude` binary double configured to invoke a declared MCP tool over HTTP
- WHEN the claudecode adapter's `RunTask` runs against it
- THEN the test asserts `ProviderResult.ActionIntents` is non-empty and was populated via the
  adapter's real MCP wiring, not a hand-constructed fixture

#### Scenario: OpenCode fake-binary test proves the real path is wired

- GIVEN a fake `opencode` binary double configured to invoke a declared MCP tool over HTTP
- WHEN the opencode adapter's `RunTask` runs against it
- THEN the test asserts `ProviderResult.ActionIntents` is non-empty and was populated via the
  adapter's real MCP wiring, not a hand-constructed fixture

**Agent roles**: all roles configured with provider `claude-code` or `opencode`. **Protocol**:
MCP over HTTP (client side); CLI-native structured output used only for text extraction.
