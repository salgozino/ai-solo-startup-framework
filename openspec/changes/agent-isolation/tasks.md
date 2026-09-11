# Tasks: Agent Isolation via --bare and System Prompt Files

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 350–420 |
| 400-line budget risk | Medium |
| Chained PRs recommended | No |
| Suggested split | Single PR (cohesive change; size:exception if reviewer requests) |
| Delivery strategy | single-pr |
| Chain strategy | size-exception |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: size-exception
400-line budget risk: Medium

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | Schema + adapters + wire + docs (full change) | PR 1 | `go test ./...` | N/A — tests use `fakeclaude`/`fakeopencode` instead of live binaries | Delete created files; revert adapter/schema/wire edits; existing `company.yaml` without `system_prompt` remains valid on rollback |

## Phase 1: Config Schema (Foundation)

- [x] 1.1 [RED — threat-matrix case e] Add failing test cases to `config/schema_test.go`: (a) missing file → load error that names the path; (b) valid relative path → resolved absolute path in `AgentConfig.SystemPrompt`; (c) absent field → empty string, no error
- [x] 1.2 Create `config/testdata/valid_system_prompt.yaml` — agent entry with `system_prompt: agents/ceo.md`
- [x] 1.3 Create `config/testdata/missing_prompt_file.yaml` — agent entry with `system_prompt: agents/missing.md` (file must not exist)
- [x] 1.4 Create `config/testdata/agents/ceo.md` — minimal prompt content for fixture (one line suffices)
- [x] 1.5 [GREEN] Modify `config/schema.go`: add `SystemPrompt string \`yaml:"system_prompt,omitempty"\`` to `AgentConfig`; add `"system_prompt"` to field whitelist switch; resolve relative path via `filepath.Join(configDir, v)` and call `os.Stat()`; return descriptive error naming the missing path when not found

## Phase 2: Test Infrastructure — Fake Binaries

- [x] 2.1 Modify `adapters/claudecode/testdata/fakeclaude/main.go`: parse `--bare`, `--no-session-persistence`, `--system-prompt-file`; echo `bare:1|` prefix in output when `--bare` is present
- [x] 2.2 Create `adapters/opencode/testdata/fakeopencode/main.go`: parse `run`, `--pure`, `--model`, `--agent`; echo `pure:1|` prefix when `--pure` is present; mirror fakeclaude's structure

## Phase 3: Claude Code Adapter

- [x] 3.1 [RED] Add `TestBareFlag_AlwaysPresent`, `TestSystemPromptFile_WhenSet`, `TestSystemPromptFile_WhenNotSet` to `adapters/claudecode/adapter_test.go`; extend `adapters/claudecode/invocation_test.go` table to assert `--bare` and `--no-session-persistence` present in every row
- [x] 3.2 [GREEN] Modify `adapters/claudecode/adapter.go`: add `systemPromptPath string` field to `Adapter`; add `systemPromptPath string` param to `New()`; in `RunTask()` always prepend `--bare --no-session-persistence`; append `--system-prompt-file <systemPromptPath>` only when non-empty

## Phase 4: OpenCode Adapter

- [x] 4.1 [RED] Create `adapters/opencode/adapter_test.go`: `TestPureFlag_AlwaysPresent`, `TestContentPrepend_WhenSet`, `TestContentPrepend_WhenNotSet`
- [x] 4.2 [GREEN] Modify `adapters/opencode/adapter.go`: add `systemPromptContent string` field; add `systemPromptPath string` param to `New()`, read file once at construction via `os.ReadFile`; in `RunTask()` always prepend `--pure`; prepend `"[SYSTEM]\n"+content+"\n\n"` to task input when content is non-empty

## Phase 5: Wire

- [x] 5.1 Modify `cmd/company/wire.go`: pass `agCfg.SystemPrompt` (resolved absolute path) as final argument to both `claudecode.New()` and `opencode.New()` in the provider switch

## Phase 6: Production Assets & Documentation

- [x] 6.1 Create `agents/ceo.md` — production CEO system prompt (role declaration, constraints, response style)
- [x] 6.2 Modify `company.yaml`: add `system_prompt: agents/ceo.md` under CEO agent entry
- [x] 6.3 Modify `README.md`: add section on isolation flags per adapter (`--bare --no-session-persistence` vs `--pure`); document `system_prompt` field, relative-path resolution, and `agents/` folder convention; replace curl as primary interaction method with CLI usage
