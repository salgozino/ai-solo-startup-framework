# Apply Progress: validate-model-startup

**Status**: complete  
**Mode**: Standard (no strict TDD — project uses pragmatic RED→GREEN with explicit test-first phases)  
**Delivery**: single-pr  
**Date**: 2026-09-07

## Completed Tasks

- [x] 1.1 fakeclaude: `--model badmodel` → stderr + exit 1; `--model hangmodel` → sleep; `fail-stderr` sentinel added
- [x] 2.1 claudecode adapter_test: `TestRunTask_StderrInError` — fail-stderr sentinel, asserts "stderr:" in error
- [x] 2.2 claudecode adapter_test: `TestRunTask_EmptyStderrOnFail` — fail sentinel (no stderr), asserts "stderr:" label still present
- [x] 2.3 opencode adapter_test: mirrors 2.1–2.2 (same sentinel pattern, fakeopencode updated)
- [x] 3.1 claudecode adapter_test: `TestProbeModel_ValidModel` — model="good", assert nil error
- [x] 3.2 claudecode adapter_test: `TestProbeModel_BadModel` — model="badmodel", assert error contains "unknown model: badmodel"
- [x] 3.3 claudecode adapter_test: `TestProbeModel_Deadline` — model="hangmodel", 1ms ctx, assert DeadlineExceeded
- [x] 3.4 opencode adapter_test: mirrors 3.1–3.3
- [x] 4.1 claudecode adapter.go RunTask: `var stderrBuf bytes.Buffer; cmd.Stderr = &stderrBuf`; error fmt `"claudecode: %w\nstderr: %s"`
- [x] 4.2 opencode adapter.go RunTask: same pattern, prefix `"opencode:"`
- [x] 5.1 claudecode adapter.go: `ProbeModel(ctx) error` — canary `["-p", "--model", model, "."]`, discard stdout, wrapped error with stderr
- [x] 5.2 opencode adapter.go: `ProbeModel(ctx) error` — canary `["run", "--model", model, "."]`, same pattern
- [x] 6.1 wire.go: `type modelProber interface { ProbeModel(ctx context.Context) error }`
- [x] 6.2 wire.go materializeAgents: type-assert `prov.(modelProber)`, 15s timeout via `context.Background()`, abort on error
- [x] 7.1 `go test ./adapters/claudecode/... -v -run "TestRunTask|TestProbeModel"` — PASS
- [x] 7.2 `go test ./adapters/opencode/... -v -run "TestRunTask|TestProbeModel"` — PASS
- [x] 7.3 `go test ./cmd/company/...` — PASS (providerOverride path skips probe; existing tests unaffected)
- [x] 7.4 `go test ./...` — full suite PASS (12 packages)

## Files Changed

| File | Action | What Was Done |
|------|--------|---------------|
| `adapters/claudecode/testdata/fakeclaude/main.go` | Modified | Added `--model badmodel` (stderr+exit1), `--model hangmodel` (sleep), `fail-stderr` sentinel |
| `adapters/opencode/testdata/fakeopencode/main.go` | Modified | Same model-level behaviours mirrored |
| `adapters/claudecode/adapter_test.go` | Modified | Added 5 tests: StderrInError, EmptyStderrOnFail, ProbeModel_Valid/Bad/Deadline; added `errors` import |
| `adapters/opencode/adapter_test.go` | Modified | Mirrored 5 tests; added `errors` import |
| `adapters/claudecode/adapter.go` | Modified | Stderr capture in RunTask; added ProbeModel method |
| `adapters/opencode/adapter.go` | Modified | Stderr capture in RunTask; added ProbeModel method |
| `cmd/company/wire.go` | Modified | Added modelProber interface; probe loop in materializeAgents |

## Key Discovery

`materializeAgents` has no context parameter — used `context.Background()` as probe parent instead of forwarding a caller ctx. This matches the startup-only semantics (15s hard deadline regardless of any outer deadline).

## Work Unit Evidence

| Evidence | Value |
|----------|-------|
| Focused test command | `go test ./adapters/claudecode/... ./adapters/opencode/... ./cmd/company/... -v -run "TestRunTask\|TestProbeModel"` → all PASS |
| Runtime harness | N/A — startup probe exercised end-to-end via fakeclaude/fakeopencode binaries inside adapter tests; existing `TestMaterialize_TwoAgentStartsTwoGoroutines` confirms providerOverride path (no probe) unchanged |
| Rollback boundary | `adapters/claudecode/adapter.go`, `adapters/opencode/adapter.go`, `cmd/company/wire.go` are independently revertible; test files and fakeclaude/fakeopencode changes are additive only |

## Deviations from Design

- **`context.Background()` in probe instead of forwarding `ctx`**: Design said `context.WithTimeout(ctx, 15*time.Second)` but `materializeAgents` has no `ctx` parameter. Used `context.Background()` which is semantically correct for a startup-only probe.
- **`fail-stderr` sentinel added to fakeclaude/fakeopencode**: Task 2.1 spec said "invoke `fail` sentinel" but `fail` has no stderr output. Added `fail-stderr` sentinel so the test actually verifies that subprocess stderr content appears in the error (stronger coverage than checking for the label with empty content).
- **`--model hangmodel` added to fakes**: Design mentioned "hang canary" for ProbeModel deadline test. Since ProbeModel's canary input is always ".", added model-level hang trigger (`--model hangmodel`) rather than input-level.

## Next Recommended

`sdd-verify` — run independent verification against specs and design.
