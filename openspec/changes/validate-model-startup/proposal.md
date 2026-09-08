# Proposal: Validate Model at Startup

## Intent

Two bugs compound to hide misconfigured models until task submission:
- Both adapters drop subprocess stderr — callers see only `exit status 1`
- Model name is never validated at startup — HTTP servers bind silently with a bad model

Fix: capture stderr everywhere, then probe model validity before servers start.

## Scope

### In Scope
- Capture stderr in both adapters; include in error wrapping on non-zero exit
- `ModelProber` interface in adapter packages (NOT `port.Provider`)
- `ProbeModel(ctx) error` on both adapters — minimal canary prompt, 15s deadline
- Call `ProbeModel` from `materializeAgents`; abort startup on failure
- Extend `fakeclaude` with `"badmodel"` sentinel for offline testing

### Out of Scope
- Static model allowlist or offline catalog validation
- Per-task model re-validation
- Changes to `port.Provider` interface or agent role protocols (CEO/CTO/engineer/designer)

## Capabilities

> No `openspec/specs/` exists yet — all capabilities are new.

### New Capabilities
- `adapter-stderr-capture`: Adapters capture subprocess stderr and surface it in errors on non-zero exit
- `model-startup-validation`: Framework validates model access before accepting tasks via `ModelProber`

### Modified Capabilities

None

## Approach

Split into two independent tasks:

**Task A (bugfix)** — Add `cmd.Stderr = &stderrBuf` in both adapters; include captured stderr in the error on non-zero exit. Ships standalone; no interface changes.

**Task B (feature)** — Define `ModelProber` in adapter packages. Implement `ProbeModel(ctx context.Context) error` on both adapters (spawns CLI with minimal canary + 15s deadline). `materializeAgents` type-asserts post-construction and aborts startup on failure. `wireOptions.providerOverride` already skips adapter construction — existing tests unaffected.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `adapters/claudecode/adapter.go` | Modified | Stderr capture + `ProbeModel` |
| `adapters/opencode/adapter.go` | Modified | Stderr capture + `ProbeModel` |
| `cmd/company/wire.go` | Modified | Call `ProbeModel` after adapter construction |
| `adapters/claudecode/testdata/fakeclaude/main.go` | Modified | Add `"badmodel"` sentinel |
| `adapters/claudecode/adapter_test.go` | Modified | Tests for stderr capture + probe |
| `adapters/opencode/adapter_test.go` | Modified | Tests for stderr capture + probe |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Startup latency (+15s worst case per agent) | Med | 15s context deadline; timeout vs bad-model are distinguishable |
| Token cost per startup (canary prompt) | Low | Minimal prompt; tradeoff documented |
| opencode CLI exits 0 for invalid models | Med | Investigate during Task B; fallback: log warning, skip abort |

## Rollback Plan

Tasks are independent. **Task A**: revert adapter files only — no interface changes, no downstream impact. **Task B**: revert `ModelProber` interface files and the `wire.go` call site — startup returns to current behavior; Task A's stderr fix remains intact.

## Dependencies

- Anthropic API access for `ProbeModel` integration test (unit tests use `fakeclaude` — no API required)

## Success Criteria

- [ ] Adapters include real CLI stderr text in returned errors on non-zero subprocess exit
- [ ] Starting with an invalid model exits before HTTP servers bind, with the real error message
- [ ] Starting with a valid model succeeds normally (latency ≤15s per agent)
- [ ] All existing `cmd/...` and `e2e` tests pass unchanged
