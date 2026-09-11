## Exploration: Agent Isolation via `--bare` and System Prompt Files

### Current State

The framework spawns CLI processes per task with no isolation:

**Claude Code** (`adapters/claudecode/adapter.go`):
```
claude -p [--model X] <input>
```
Inherits: user hooks, LSP integration, all MCP servers, CLAUDE.md from the user's home directory, persona skills, auto-memory, session persistence, and all local config. Company agents behave as whatever the user has configured on their machine — not as the declared company role.

**OpenCode** (`adapters/opencode/adapter.go`):
```
opencode run [--model X] [--agent X] <input>
```
The `--agent X` flag selects a named agent from the user's `~/.config/opencode/` config, which is still user-local. Full system prompt injection via CLI flag does not exist in `opencode run`.

**Config schema** (`config/schema.go`): `AgentConfig` has `Name`, `Role`, `Provider`, `Model`. Unknown fields are rejected at load time via a strict whitelist in `Load()` (line 99). No `system_prompt` field exists.

**Wire** (`cmd/company/wire.go`): `materializeAgents` creates adapters directly from `agCfg`. No system prompt or isolation flags are passed.

### Affected Areas

- `adapters/claudecode/adapter.go` — must add `--bare`, `--no-session-persistence`, `--system-prompt-file` to argv construction; `Adapter` struct gains a `systemPromptPath` field; `New()` gains a path parameter
- `adapters/opencode/adapter.go` — must add `--pure` for partial isolation; system prompt content prepended to input (no CLI flag equivalent)
- `config/schema.go` — `AgentConfig` gains `SystemPrompt string`; `Load()` must add `"system_prompt"` to the valid-field whitelist and validate the file exists when set; file path resolved to absolute relative to the company.yaml directory
- `cmd/company/wire.go` — pass `agCfg.SystemPrompt` (resolved path) to both adapter `New()` constructors
- `adapters/claudecode/invocation_test.go` / `adapter_test.go` — test that `--bare`, `--no-session-persistence`, `--system-prompt-file` are present in the invoked argv when set; the `fakeclaude` helper binary may need to handle the new flags
- `config/schema_test.go` and `config/testdata/` — add positive test for `system_prompt` field; add negative test for missing file reference
- `README.md` — document `system_prompt` field, explain isolation mechanism, update testing instructions (curl-based example is outdated; UI is preferred for human interaction)

### Flag Verification

Both flags are confirmed present in the installed Claude Code CLI:

| Flag | Verified | Notes |
|------|---------|-------|
| `--bare` | ✅ | "Minimal mode: skip hooks, LSP, plugin sync, attribution, auto-memory" |
| `--system-prompt-file <path>` | ✅ | Accepted by `claude -p --system-prompt-file /dev/null "test"` without error |
| `--no-session-persistence` | ✅ | Listed in `--help` output |
| `--strict-mcp-config` | ✅ | Listed in `--help` output |

OpenCode `run` subcommand:
| Flag | Status | Notes |
|------|--------|-------|
| `--pure` | ✅ | "run without external plugins" — partial isolation |
| `--system-prompt-file` | ❌ | Not supported in `opencode run` |
| `--system-prompt` | ❌ | Not supported in `opencode run` |

### Approaches

1. **File path passed to Claude Code; content prepended for OpenCode** _(recommended)_
   - `AgentConfig.SystemPrompt` holds a relative path (resolved to absolute at `Load()` time)
   - Claude Code: `--bare --no-session-persistence [--system-prompt-file <abs-path>] -p [--model X] <input>`
   - OpenCode: `--pure [--model X] [--agent X] <input>` with system prompt content prepended inline to input as `[SYSTEM]\n{content}\n\n{input}` when set
   - Pros: Uses the native mechanism for Claude Code (file read by the CLI, not by Go code); works for OpenCode via content injection; both providers get the isolation flags regardless of whether system_prompt is set
   - Cons: OpenCode content injection is a workaround; large prompts slightly increase argument length (not a real concern since argv slices handle MBs and system prompts are typically <50KB)
   - Effort: Medium

2. **Read content at `Load()` time; pass inline `--system-prompt <content>` for Claude Code**
   - `AgentConfig` stores the resolved content string (not the path)
   - Claude Code: `--bare --no-session-persistence [--system-prompt <content>] -p [--model X] <input>`
   - OpenCode: same as Approach 1 (prepend to input)
   - Pros: Single resolution point; content validated at startup
   - Cons: Large system prompts become huge Go string constants; any file change requires restart (same as Approach 1); `claude` must parse content from argv rather than a file (minor inefficiency)
   - Effort: Medium (same as #1 really)

3. **Skip OpenCode system prompt injection; document as limitation**
   - Same as Approach 1 for Claude Code; for OpenCode, `--pure` only — no system prompt injection
   - Pros: Simpler implementation; avoids the OpenCode content-prepend hack
   - Cons: OpenCode agents cannot be configured with role-specific system prompts — this kills a primary use case for the feature
   - Effort: Low

### Recommendation

**Approach 1**: File path for Claude Code + content-prepend for OpenCode.

Key rationale:
- `--system-prompt-file` is the right primitive for Claude Code — it avoids argv escaping concerns and is the idiomatic way to pass large prompts.
- Content-prepend for OpenCode is a known pattern in the LLM space and works until OpenCode adds native CLI support.
- Both adapters should always apply isolation flags (`--bare` / `--pure` + `--no-session-persistence`) independent of whether `system_prompt` is set. Isolation is per-provider baseline behavior once this feature ships.
- `system_prompt` should be optional. When absent, no `--system-prompt-file` is appended and no content is prepended. The isolation flags still apply.
- File path should be relative to the directory containing `company.yaml`. `Load()` already receives the file path, so `filepath.Dir(path)` gives the base. The resolved absolute path is stored on `AgentConfig` — adapters receive the already-resolved path, not the raw user string.
- Validate file existence at `Load()` time (fail fast). Do not read content at load time to keep startup memory footprint low.

Additional `--bare` companions to include by default:
- `--no-session-persistence` — prevents cross-run session state leakage (high value, low cost)
- `--strict-mcp-config` — optional; only useful if the company declares specific MCP servers. Skip for now; add as a follow-up when MCP config support is added.

### Risks

- **`--system-prompt-file` flag stability**: The flag is present in the current Claude Code version but is not part of a public API contract. If Anthropic removes or renames it, all agents with `system_prompt` silently fall back to wrong behavior (non-zero exit, caught by the adapter). Mitigation: integration test in CI that invokes `claude --help | grep system-prompt-file`.
- **OpenCode content-prepend semantics**: Prepending `[SYSTEM]` to the user input is a convention, not a contract. The model may not follow it as strictly as a true system prompt. Mitigation: document clearly that OpenCode system prompt support is best-effort; recommend Claude Code for agents requiring strict persona enforcement.
- **Config validation tightness**: Adding `system_prompt` to the whitelist alongside relaxing strict unknown-field detection risks allowing other sneaky fields. The whitelist must be explicitly maintained (it already is; just add `"system_prompt"`).
- **File path portability**: If `company.yaml` is checked into a repo and the `agents/` folder is in the same repo, relative paths work universally. Absolute paths break across machines. Enforce relative paths in documentation and validation.
- **README curl example**: The README shows a raw `curl /invoke` command as the primary interaction method. This is misleading — the UI is the intended interface for human interaction. A confusing README may cause users to skip the approval flow and attempt direct A2A injection.

### Design Decisions Confirmed

| Question | Answer |
|----------|--------|
| Is `system_prompt` required or optional? | Optional. When absent, adapter runs with isolation flags only. |
| Validate file at config load time? | Yes — fail fast with a clear error message. |
| Adapter receives path or content? | Path (resolved to absolute). Adapter passes it to `--system-prompt-file`. |
| Where does `agents/` folder live? | Relative to `company.yaml`; convention but not enforced by the schema. |
| Always apply `--bare` / `--pure`? | Yes — isolation is baseline behavior once this change ships, not opt-in per agent. |
| `--no-session-persistence`? | Yes — always applied for Claude Code. |
| `--strict-mcp-config`? | No — defer until MCP config support is added to the schema. |

### Ready for Proposal

Yes. The exploration is complete. The orchestrator should tell the user:

> Explored agent isolation for both providers. Claude Code gets `--bare --no-session-persistence [--system-prompt-file]`; OpenCode gets `--pure` plus content-prepend for system prompts (no native CLI support). Config schema gains an optional `system_prompt` field validated at load time. README testing instructions need updating. Ready to write the proposal when you confirm the approach.
