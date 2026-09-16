# Design: Validate Model at Startup

## Technical Approach

Two sequential tasks. **Task A** adds `cmd.Stderr = &stderrBuf` to both adapter `RunTask` methods — minimum-diff bugfix, no interface changes. **Task B** adds `ProbeModel(ctx) error` to both adapters; `materializeAgents` type-asserts a local `modelProber` interface and calls it before any supervisor or server is constructed. `wireOptions.providerOverride` already skips real adapter construction, so all existing tests are unaffected without additional guards.

## Architecture Decisions

| # | Question | Options | Decision | Rationale |
|---|----------|---------|----------|-----------|
| 1 | Where to declare `ModelProber` interface | (A) In each adapter package — two definitions, wire imports both | **(B) Unexported `modelProber` in `wire.go`** | Go structural typing: single consumer-side definition; no new package coupling; idiomatic (define interfaces at point of use) |
| 2 | Stderr capture with `StdoutPipe` | (A) Switch to `cmd.Output()` — drops LimitReader/drain pattern | **(B) Keep `StdoutPipe`, set `cmd.Stderr = &stderrBuf` before `cmd.Start()`** | `cmd.Stderr` and `StdoutPipe()` are independent; zero regression risk to existing read/drain logic |
| 3 | `fakeclaude` badmodel detection | (A) New input sentinel ("probe-badmodel") | **(B) Check `--model badmodel` flag, exit 1 + write to stderr** | Model invalidity is a model concern, not a prompt concern; tests pass `claudecode.New(bin, opts, "badmodel")` and call `ProbeModel` naturally |
| 4 | Probe concurrency in `materializeAgents` | (A) Parallel goroutines per agent | **(B) Sequential in existing loop** | Simpler; error message names the failing agent; 15s deadline per probe is acceptable at startup only |

## Data Flow

**Task A — stderr capture in RunTask:**

```
RunTask(ctx, taskID, input)
  cmd.Stderr = &stderrBuf          ← new (before cmd.Start)
  cmd.StdoutPipe() → stdout
  cmd.Start()
  io.LimitReader(stdout) → buf     ← unchanged
  io.Discard drain                 ← unchanged
  cmd.Wait() → waitErr
  waitErr != nil
    → fmt.Errorf("claudecode: %w\nstderr: %s", waitErr, stderrBuf.String())  ← new
```

**Task B — startup probe in materializeAgents:**

```
materializeAgents loop (per agent)
  providerOverride == nil
    → construct prov (claudecode.New / opencode.New)  ← unchanged
    → prov.(modelProber) → prober, ok                 ← new
    → context.WithTimeout(ctx, 15s) → probeCtx        ← new
    → prober.ProbeModel(probeCtx) → err               ← new
    → err != nil → return nil, fmt.Errorf(...)        ← new (abort before any server binds)
  supervisor.New(...)                                  ← unchanged
  transa2a.New(...)                                    ← unchanged
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `adapters/claudecode/adapter.go` | Modify | Stderr capture in `RunTask`; add `ProbeModel` method |
| `adapters/opencode/adapter.go` | Modify | Stderr capture in `RunTask`; add `ProbeModel` method |
| `cmd/company/wire.go` | Modify | Add unexported `modelProber` interface; call `ProbeModel` in loop |
| `adapters/claudecode/testdata/fakeclaude/main.go` | Modify | Add `--model badmodel` → exit 1 + stderr branch |
| `adapters/claudecode/adapter_test.go` | Modify | RED tests: stderr in error on fail; `ProbeModel` happy/bad-model paths |
| `adapters/opencode/adapter_test.go` | Modify | Same RED tests for opencode adapter |

## Interfaces / Contracts

```go
// wire.go — local, unexported; satisfied structurally by both adapters
type modelProber interface {
    ProbeModel(ctx context.Context) error
}

// claudecode/adapter.go (and opencode/adapter.go — identical signature)
// ProbeModel spawns a minimal canary invocation with a 15s-bounded ctx.
// Returns non-nil if the model is inaccessible; stderr is included in the error.
func (a *Adapter) ProbeModel(ctx context.Context) error

// Error format on subprocess failure (Task A + Task B shared):
// "claudecode: exit status 1\nstderr: <captured text>"
```

`ProbeModel` canary args for claudecode: `["-p", "--model", model, "."]`  
`ProbeModel` canary args for opencode: `["run", "--model", model, "."]`  
(model flag omitted when `a.model == ""`; empty-model adapters skip the flag, not the probe)

## Testing Strategy

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit | Stderr appears in error on non-zero exit | `fakeclaude` `fail` sentinel; assert `strings.Contains(err.Error(), "stderr:")` |
| Unit | Empty stderr on failure doesn't panic | `fakeclaude` `fail` + no stderr write; assert error non-nil, no crash |
| Unit | Success discards stderr silently | `fakeclaude` default path; assert no error |
| Unit | `ProbeModel` succeeds for valid model | `fakeclaude` with `--model good`; assert nil error |
| Unit | `ProbeModel` fails for bad model | `fakeclaude` with `--model badmodel`; assert non-nil error containing stderr text |
| Unit | `ProbeModel` honours context deadline | Short-deadline ctx + `hang` canary; assert deadline-exceeded error |
| Integration | `materializeAgents` aborts on bad model | `wireOptions` without override, fakeclaude binary, `--model badmodel` in config |
| Integration | `materializeAgents` succeeds with valid model | Same path, valid model |

## Threat Matrix

This design modifies subprocess integration (exec.CommandContext). Existing threat-matrix cases from `adapters/claudecode/adapter_test.go` remain covered and unchanged:

| Case | Description | Status |
|------|-------------|--------|
| (a) argv injection | Model name and canary come from config/constant, not user runtime input; argv-as-slice preserved | Applicable — existing RED test covers RunTask; ProbeModel follows same pattern |
| (b) hung child | 15s `context.WithTimeout` passed to `exec.CommandContext`; SIGKILL on deadline | Applicable — new RED test for ProbeModel deadline |
| (c) oversized output | ProbeModel reads and discards stdout (canary output is irrelevant) | N/A for probe — probe only checks exit code + stderr; no size cap needed |
| (d) non-zero exit | ProbeModel returns error on waitErr != nil; stderr included | Applicable — new RED test for ProbeModel bad-model path |

## Migration / Rollout

No migration required. Task A is a drop-in error-message improvement (no interface changes). Task B aborts startup on bad model — new behavior, but only triggered by misconfiguration that previously produced silent failures at task submission time. Rollback per the proposal: revert each task's files independently.

## Open Questions

- [ ] Does `opencode run` exit non-zero for an invalid model name, or does it exit 0 with an error message on stdout? (Risk: Medium — identified in proposal. Design assumes non-zero exit. If opencode exits 0, `ProbeModel` must parse stdout for error markers; that path is deferred to implementation discovery.)
