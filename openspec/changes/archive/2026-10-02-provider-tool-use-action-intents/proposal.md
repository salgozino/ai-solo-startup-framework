# Proposal: Provider Tool Use and Action Intent Emission via Native MCP

## Intent

Real adapters never populate `ProviderResult.ActionIntents`, so tasks complete with zero side effects (issue #50). The `provider-adapter` requirement "Providers Declare Their Capabilities" was never implemented (verify C-1): `ProviderCapabilities` has no Go definition; `ContextBudget` is supervisor-owned (`core/supervisor/supervisor.go:29`). One boundary: providers declare what they can do and emit intents through it.

## Scope

### In Scope
- `ProviderCapabilities{ContextBudget, ActionKinds}` + `Capabilities()` on `port.Provider`; `fake.Provider` conforms.
- Single long-lived HTTP MCP server, one tool per `risk_policy` kind; `tools/call` records an intent and acknowledges without executing.
- Per-invocation bearer token header resolved to `{tenant, agent, taskID}`; mirrors `transport/a2a` auth-then-tenant interceptors.
- Adapters: ephemeral MCP config (claude `--mcp-config --strict-mcp-config`, `--output-format stream-json --verbose`; opencode `OPENCODE_CONFIG_CONTENT`, `--format json`).
- `Config.ContextBudget` becomes an optional override.
- `cmd/company` wiring; fake binaries that call the server over HTTP.

### Out of Scope
- C-2 (`POST /api/send`) — foundation change.
- Prompt-convention parsing, claude `--json-schema` (ends the turn), stdio servers.

## Capabilities

### New Capabilities
- `mcp-tool-server`: HTTP MCP server, tool registry from risk policy, token binding, tenant boundary, acknowledge-without-execute.
- `provider-action-intent-emission`: adapters declare tools natively and return recorded intents.

### Modified Capabilities
- `provider-adapter` (pending archive in `agent-startup-framework-foundation`): concrete capability contract and budget ownership.

## Approach

1. Server owns the invocation registry: adapter mints a token before spawn, releases after exit; unknown, expired, or foreign-tenant tokens are rejected before any handler.
2. Intents come from the authenticated server sink, not from parsing `tool_use` events — sidestepping CLI divergence (`mcp__s__t` vs `s_t`, inline vs separate results, no terminal event in opencode). Stream parsing extracts text only.
3. **Known tension**: MCP `tools/call` is synchronous and expects execution. This framework records, classifies via `PolicyEngine`, may escalate, and executes later via `Gateway`. The handler acknowledges instead — an inversion of MCP semantics, accepted by the human; tool descriptions must state it.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `core/port/provider.go`, `core/port/fake/` | Modified | Capabilities contract |
| `transport/mcp/` | New | Server, auth/tenant, intent sink |
| `adapters/{claudecode,opencode}/` | Modified | MCP config, structured output |
| `core/supervisor/supervisor.go` | Modified | Budget override |
| `cmd/company/`, `go.mod` | Modified | Server lifecycle, MCP dependency |

**Agent roles**: all roles with `risk_policy` entries.

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| CLI wire divergence inflates cost | High | Sink is source of truth |
| Model retries after acknowledgment | Med | Per-token dedupe; explicit description |
| Cross-tenant or leaked token | Low | Single-use, TTL, constant-time compare |
| opencode `--format json` floor unknown | Med | Document "1.18.31 verified" |
| Silent no-op regression | High | Fake binaries hit the real server; e2e asserts intents |

## Rollback Plan

Revert the PR; adapters return to text-only `RunTask`, no persisted formats change. Server startup failure aborts `materialize` before binding.

## Dependencies

- `claude` >= 2.1.268, `opencode` >= 1.18.31 (verified locally only); an MCP Go implementation (design decides).

## Size Forecast

**Exceeds the 400-line budget**: estimated 1,200–1,800 changed lines. `single-pr` conflicts; the human must choose `size:exception` or chained PRs before apply.

## Success Criteria

- [ ] `Capabilities()` on all providers; supervisor honors declared budget unless overridden.
- [ ] Real subprocess calls MCP tool over HTTP → `ActionIntents` non-empty → policy classifies.
- [ ] Bad token or foreign tenant rejected; no intent recorded.
- [ ] `tools/call` never calls `Gateway.Send`; `go test ./...` green.
