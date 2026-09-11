# Design: Agent Isolation via --bare and System Prompt Files

## Technical Approach

Apply isolation flags unconditionally to both adapters (no opt-in). Thread an optional `system_prompt` file path through config → wire → adapter constructors. The path is validated and resolved to absolute at `Load()` time; adapters receive clean paths (Claude Code) or pre-read content (OpenCode). No changes to `core/`, `supervisor`, `port` interfaces, or `transport/`.

## Architecture Decisions

| Decision | Choice | Alternative | Rationale |
|----------|--------|-------------|-----------|
| Where to validate & resolve path | `config.Load()` | `wire.go` | Matches existing fail-fast pattern (inline-secret guard). Adapters receive clean values, not raw config strings. |
| OpenCode: path or content in Adapter | Read content once at `New()` | Read file per `RunTask()` | File read once at startup; adapter stays stateless per-invocation (existing pattern). |
| Isolation flags: opt-in or unconditional | Unconditional | Config toggle | No valid use case for non-isolated agents in this framework — isolation is a security baseline, not a feature. |
| Adapter constructor style | Add `systemPromptPath string` param directly | Wrap into `Options` struct | Matches existing `New()` style for both adapters; avoids Options struct version churn for a single new param. |

## Data Flow

```
company.yaml ──Load()──► AgentConfig.SystemPrompt (abs path or "")
                                  │
                          wire.go materializeAgents()
                                  │
              ┌───────────────────┴────────────────────────┐
     claudecode.New(…, systemPromptPath)      opencode.New(…, systemPromptPath)
              │                                            │
       stores abs path                         reads file → stores content string
              │                                            │
       RunTask(): builds argv                   RunTask(): prepends content to input
         -p --bare --no-session-persistence       run --pure [--model M] [--agent N]
         [--model M] [--system-prompt-file P]     input = "[SYSTEM]\n{content}\n\n" + task
         <input>                                  (when systemPromptContent != "")
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `config/schema.go` | Modify | `SystemPrompt string` on `AgentConfig`; `"system_prompt"` added to whitelist switch; path resolved to abs + file existence validated at `Load()` |
| `adapters/claudecode/adapter.go` | Modify | `Adapter` gains `systemPromptPath string`; `New()` gains `systemPromptPath string` param; `RunTask()` always prepends `--bare --no-session-persistence`; appends `--system-prompt-file <path>` when set |
| `adapters/opencode/adapter.go` | Modify | `Adapter` gains `systemPromptContent string`; `New()` gains `systemPromptPath string`, reads content; `RunTask()` always prepends `--pure`; prepends `[SYSTEM]\n{content}\n\n` to input when set |
| `cmd/company/wire.go` | Modify | Pass `agCfg.SystemPrompt` (resolved absolute path) to both `New()` calls in provider switch |
| `adapters/claudecode/testdata/fakeclaude/main.go` | Modify | Consume `--bare`, `--no-session-persistence`, `--system-prompt-file`; echo `bare:1\|` prefix when `--bare` is present so tests can assert isolation |
| `adapters/claudecode/adapter_test.go` | Modify | Add `TestBareFlag_AlwaysPresent`, `TestSystemPromptFile_WhenSet`, `TestSystemPromptFile_WhenNotSet` |
| `adapters/claudecode/invocation_test.go` | Modify | Extend table to assert isolation flags present in every invocation |
| `adapters/opencode/adapter_test.go` | Create | `TestPureFlag_AlwaysPresent`, `TestContentPrepend_WhenSet`, `TestContentPrepend_WhenNotSet` — mirrors claudecode test pattern with `fakeopencode` |
| `adapters/opencode/testdata/fakeopencode/main.go` | Create | `fakeopencode` binary: parses `run`, `--pure`, `--model`, `--agent`; echoes `pure:1\|` when `--pure` present |
| `config/schema_test.go` | Modify | Add cases: valid `system_prompt` accepted; missing file rejected with clear error; resolved path is absolute |
| `config/testdata/valid_system_prompt.yaml` | Create | Fixture with `system_prompt: agents/ceo.md` (relative path, resolved at load) |
| `config/testdata/missing_prompt_file.yaml` | Create | Fixture referencing nonexistent path → expect load error |
| `config/testdata/agents/ceo.md` | Create | Minimal prompt file for schema test fixture |
| `agents/ceo.md` | Create | Production system prompt for CEO agent role |
| `company.yaml` | Modify | Add `system_prompt: agents/ceo.md` to CEO agent entry |
| `README.md` | Modify | Document `system_prompt`, isolation behavior, `agents/` folder convention; replace curl as primary interaction method |

## Interfaces / Contracts

```go
// config/schema.go — AgentConfig extended
type AgentConfig struct {
    Name         string `yaml:"name"`
    Role         string `yaml:"role"`
    Provider     string `yaml:"provider"`
    Model        string `yaml:"model,omitempty"`
    SystemPrompt string `yaml:"system_prompt,omitempty"` // abs path after Load(); "" if unset
}

// adapters/claudecode — updated New() signature
func New(claudeBin string, opts Options, model string, systemPromptPath string) *Adapter

// adapters/opencode — updated New() signature
func New(opencodeBin string, opts Options, model string, agentName string, systemPromptPath string) *Adapter
```

**Claude Code argv (isolation unconditional):**
```
claude -p --bare --no-session-persistence [--model M] [--system-prompt-file P] <input>
```

**OpenCode argv (isolation unconditional):**
```
opencode run --pure [--model M] [--agent N] <effective-input>
```
where `effective-input` = `"[SYSTEM]\n{content}\n\n" + task` when `systemPromptContent != ""`, else `task` unchanged.

## Testing Strategy

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit — config | `system_prompt` accepted; missing file → load error; resolved path is absolute | New YAML fixtures + schema test cases |
| Unit — claudecode | `--bare` always in argv; `--system-prompt-file` only when set; injection still blocked | Updated `fakeclaude` echoes `bare:1\|`; new test cases in `adapter_test.go` |
| Unit — opencode | `--pure` always in argv; content prepend only when `systemPromptContent != ""`; no-prompt path unchanged | New `fakeopencode` binary; new `adapter_test.go` mirroring claudecode pattern |
| Unit — wire | Covered by schema + adapter unit tests | No new wire unit tests |

## Threat Matrix

No `references/threat-matrix.md` in this project. Threat cases are documented inline in `adapter_test.go`. Assessment for this change:

| Case | Applicable? | Notes |
|------|-------------|-------|
| (a) argv injection via user input | N/A | New flags are hardcoded strings or config-validated abs paths. Never constructed from task input at `RunTask()` time. Existing RED tests unchanged. |
| (b) hung child killed on deadline | N/A | No change to deadline/kill logic. |
| (c) oversized output truncation | N/A | No change to output path. |
| (d) non-zero exit → error | N/A | No change to exit handling. |
| (e) path injection via `system_prompt` config | **Applicable** | Mitigated: path resolved and file-existence-checked at `Load()` time; `--system-prompt-file` receives a static pre-resolved abs path, not a runtime-constructed string. |

**RED test for (e)**: `TestSystemPromptPath_NeverConstructedAtRunTime` — `Load()` with a missing file returns an error; valid path produces `--system-prompt-file <abs-path>` in argv with no runtime concatenation.

## Migration / Rollout

No migration required. `system_prompt` is `omitempty` — existing `company.yaml` files without it continue to load and run. Isolation flags (`--bare`, `--pure`) activate silently for all agents on upgrade with no config changes needed.

## Open Questions

- [ ] Verify `opencode run --pure --agent <name>` does not conflict (brief CLI test before implementation).
