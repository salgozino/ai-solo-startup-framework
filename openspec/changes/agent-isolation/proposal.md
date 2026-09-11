# Proposal: Agent Isolation via --bare and System Prompt Files

## Intent

Agents inherit the user's full local environment (hooks, MCP servers, CLAUDE.md, persona, session state). Declared roles are unreliable — a "legal advisor" behaves as the user's personal assistant. Add isolation flags to both adapters and an optional per-agent system prompt so each agent runs as its declared role.

## Scope

### In Scope
- `--bare --no-session-persistence [--system-prompt-file]` for Claude Code adapter
- `--pure` + content-prepend workaround for OpenCode adapter
- Optional `system_prompt` field in `AgentConfig` (path-based, validated at load time)
- Wire: resolve absolute path, pass to adapter constructors
- `agents/` folder with prompt MDs; `company.yaml` and README updated

### Out of Scope
- MCP server injection, `--strict-mcp-config` — deferred to future change
- Permission mode, output format, A2A transport, supervisor logic, gateway

## Capabilities

> Contract for sdd-spec. `openspec/specs/` is empty — no existing capabilities to modify.

### New Capabilities
- `agent-isolation`: unconditional process isolation for all agents; Claude Code via `--bare --no-session-persistence`, OpenCode via `--pure`
- `system-prompt-config`: optional `system_prompt` on `AgentConfig`; relative path resolved to absolute at load time; file existence validated; passed as `--system-prompt-file` (Claude Code) or content-prepended to input (OpenCode)

### Modified Capabilities
None

## Approach

1. `config/schema.go` — add `SystemPrompt string` to `AgentConfig`, add `"system_prompt"` to valid-field whitelist, validate referenced file exists at load time
2. `cmd/company/wire.go` — resolve path relative to `company.yaml` dir; pass to adapter constructors
3. `adapters/claudecode/adapter.go` — always prepend `--bare --no-session-persistence`; append `--system-prompt-file <path>` when set
4. `adapters/opencode/adapter.go` — always prepend `--pure`; prepend `[SYSTEM]\n{content}\n\n` to input when set
5. Create `agents/` folder, prompt MDs, update `company.yaml`, update README

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `config/schema.go` | Modified | `SystemPrompt` field + whitelist entry + load-time file validation |
| `adapters/claudecode/adapter.go` | Modified | Isolation flags always; `--system-prompt-file` when set |
| `adapters/opencode/adapter.go` | Modified | `--pure` always; content-prepend when set |
| `cmd/company/wire.go` | Modified | Path resolution + pass resolved path to constructors |
| `config/schema_test.go` + testdata | Modified | Positive/negative tests for `system_prompt` field |
| `adapters/claudecode/adapter_test.go` | Modified | Assert new flags in constructed argv |
| `adapters/claudecode/invocation_test.go` | Modified | Assert `--bare` always present |
| `agents/*.md` | New | System prompt files for declared agent roles |
| `company.yaml` | Modified | `system_prompt` paths per agent entry |
| `README.md` | Modified | Document mechanism; replace curl example with UI instructions |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| `--system-prompt-file` removed by Anthropic | Low | CI check: `claude --help \| grep system-prompt-file` |
| OpenCode content-prepend not honored as true system prompt | Med | Document as best-effort; recommend Claude Code for strict persona enforcement |
| Absolute paths break across machines | Low | Enforce relative paths in docs + validation error message |

## Rollback Plan

`git revert` the schema + adapter + wire commits. `system_prompt` is optional — no data migration. Prompt files in `agents/` are inert without adapter code and can remain.

## Dependencies

- Claude Code CLI with `--bare` and `--system-prompt-file` (verified ✅)
- OpenCode CLI with `--pure` (verified ✅)

## Success Criteria

- [ ] Claude Code invocation always includes `--bare --no-session-persistence`
- [ ] Agent with `system_prompt: agents/x.md` receives that file's content as its system prompt
- [ ] Agent without `system_prompt` runs with isolation flags only (no error)
- [ ] `company.yaml` referencing a missing file fails at load with a clear error message
- [ ] Unknown fields in `company.yaml` still fail validation
- [ ] README documents the mechanism and removes curl as primary interaction method
