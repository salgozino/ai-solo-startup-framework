# Tasks: Validate Model at Startup

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 170–210 |
| 400-line budget risk | Low |
| Chained PRs recommended | No |
| Suggested split | Single PR |
| Delivery strategy | single-pr |
| Chain strategy | pending |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: pending
400-line budget risk: Low

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | Task A + B as one reviewable slice | PR 1 | `go test ./adapters/... ./cmd/company/...` | N/A — startup probe exercised via fakeclaude binary inside adapter tests; no live model needed | Revert `adapters/claudecode/adapter.go`, `adapters/opencode/adapter.go`, `cmd/company/wire.go` independently; Tasks A and B are independently revertible |

## Phase 1: Foundation (fakeclaude Test Double)

- [x] 1.1 `adapters/claudecode/testdata/fakeclaude/main.go`: add `--model` flag parsing; when `model == "badmodel"` write "unknown model: badmodel" to stderr and `os.Exit(1)`

## Phase 2: RED Tests — Stderr Capture (Task A)

- [x] 2.1 [RED] `adapters/claudecode/adapter_test.go`: add `TestRunTask_StderrInError` — invoke fakeclaude `fail` sentinel; assert `strings.Contains(err.Error(), "stderr:")` (spec: Subprocess fails with stderr output)
- [x] 2.2 [RED] `adapters/claudecode/adapter_test.go`: add `TestRunTask_EmptyStderrOnFail` — fakeclaude `fail` with no stderr write; assert non-nil error without panic (spec: Subprocess fails with empty stderr)
- [x] 2.3 [RED] `adapters/opencode/adapter_test.go`: mirror tasks 2.1–2.2 for opencode adapter using its test double

## Phase 3: RED Tests — ProbeModel (Task B + threat matrix (b) and (d))

- [x] 3.1 [RED] `adapters/claudecode/adapter_test.go`: add `TestProbeModel_ValidModel` — `--model good`; assert nil error (spec: Adapter exposes ProbeModel / valid model succeeds)
- [x] 3.2 [RED] `adapters/claudecode/adapter_test.go`: add `TestProbeModel_BadModel` — `--model badmodel`; assert non-nil error containing "stderr:" (threat (d); spec: Invalid model — probe fails with stderr)
- [x] 3.3 [RED] `adapters/claudecode/adapter_test.go`: add `TestProbeModel_Deadline` — 1ms ctx deadline + hang canary; assert deadline-exceeded error (threat (b); spec: Probe exceeds 15-second deadline)
- [x] 3.4 [RED] `adapters/opencode/adapter_test.go`: mirror tasks 3.1–3.3 for opencode adapter

## Phase 4: GREEN — Stderr Capture (Task A)

- [x] 4.1 `adapters/claudecode/adapter.go` `RunTask`: declare `var stderrBuf bytes.Buffer`; assign `cmd.Stderr = &stderrBuf` before `cmd.Start()`; on `waitErr != nil` return `fmt.Errorf("claudecode: %w\nstderr: %s", waitErr, stderrBuf.String())`
- [x] 4.2 `adapters/opencode/adapter.go` `RunTask`: same pattern; error prefix `"opencode:"`

## Phase 5: GREEN — ProbeModel (Task B)

- [x] 5.1 `adapters/claudecode/adapter.go`: add `ProbeModel(ctx context.Context) error`; canary args `["-p", "--model", a.model, "."]` (omit `--model` pair when `a.model == ""`); capture stderr; return wrapped error on non-zero exit
- [x] 5.2 `adapters/opencode/adapter.go`: add `ProbeModel(ctx context.Context) error`; canary args `["run", "--model", a.model, "."]`; same stderr/error pattern

## Phase 6: Wire Integration

- [x] 6.1 `cmd/company/wire.go`: add unexported `type modelProber interface { ProbeModel(ctx context.Context) error }`
- [x] 6.2 `cmd/company/wire.go` `materializeAgents`: inside `providerOverride == nil` branch, after `prov` construction, type-assert `prov.(modelProber)`; create `probeCtx` via `context.WithTimeout(context.Background(), 15*time.Second)`; call `prober.ProbeModel(probeCtx)`; on error return `nil, fmt.Errorf("agent %q model probe failed: %w", name, err)`

## Phase 7: Verify

- [x] 7.1 `go test ./adapters/claudecode/... -v -run "TestRunTask|TestProbeModel"` — all pass
- [x] 7.2 `go test ./adapters/opencode/... -v -run "TestRunTask|TestProbeModel"` — all pass
- [x] 7.3 `go test ./cmd/company/...` — existing tests unaffected (providerOverride path unchanged)
- [x] 7.4 `go test ./...` — full suite green
