# Exploration: validate-model-startup

**Change**: validate-model-startup  
**Date**: 2026-09-07  
**Status**: Ready for Proposal

---

## Current State

`materializeAgents` in `cmd/company/wire.go` (line 143) constructs one `agentRuntime` per agent in `company.yaml`. For each agent it instantiates a concrete adapter:

```go
case "claude-code":
    prov = claudecode.New("claude", claudecode.Options{}, agCfg.Model)
case "opencode":
    prov = opencode.New("opencode", opencode.Options{}, agCfg.Model, agCfg.Name)
```

Both `claudecode.New` and `opencode.New` are constructors that only store the model string — no validation occurs. The model name is first used when `RunTask` spawns the CLI subprocess:

- **claudecode**: `claude -p [--model <model>] <input>`
- **opencode**: `opencode run [--model <model>] [--agent <name>] <input>`

**Two compounding bugs exist:**

1. **Late validation**: The model name is never checked until the first task is executed (after HTTP servers are up and the user submits work).
2. **Silent stderr**: Both adapters use `cmd.StdoutPipe()` only. Stderr is inherited from the parent process and is NOT captured. When `claude` exits 1 for an invalid model, the supervisor sees `claudecode: exit status 1` — the actual Claude error message ("There's an issue with the selected model…") is lost.

**Verified CLI behavior** (tested live):

```
$ claude -p --model "anthropic/claude-sonnet-99-invalid" "test"
[stderr] "anthropic/claude-sonnet-99-invalid" isn't described by this version's model catalog...
[stderr] There's an issue with the selected model (anthropic/claude-sonnet-99-invalid).
         It may not exist or you may not have access to it.
exit: 1
```

The error is clear on stderr, but the adapter throws it away.

---

## Affected Areas

- `adapters/claudecode/adapter.go` — primary fix: stderr capture + `ProbeModel` method
- `adapters/opencode/adapter.go` — same: stderr capture + `ProbeModel` method
- `cmd/company/wire.go` — call `ProbeModel` from `materializeAgents` after adapter construction
- `adapters/claudecode/testdata/fakeclaude/main.go` — extend fake binary to simulate model errors on stderr
- `adapters/claudecode/adapter_test.go` — new tests for `ProbeModel` and stderr capture
- `adapters/opencode/adapter_test.go` — same

---

## Approaches

### 1. Fix stderr capture only (deferred validation)

Add `cmd.Stderr = &stderrBuf` in `RunTask`, include captured stderr in the returned error when the subprocess exits non-zero.

- **Pros**: Minimal change; no API call at startup; always useful regardless of validation strategy; fixes the opaque-error UX issue now
- **Cons**: Does NOT fail fast — user still must submit a task to discover a bad model
- **Effort**: Low

---

### 2. Static model allowlist in config (offline validation)

Add a map of valid model name patterns per provider to `config/schema.go`. Validate during `config.Load()`.

- **Pros**: Zero API calls; no network required; cheapest possible check
- **Cons**: Maintenance burden (allowlist goes stale as Anthropic releases models); cannot detect models you are not entitled to access; false negatives for new valid models
- **Effort**: Low

---

### 3. `ModelProber` interface + startup probe (RECOMMENDED)

Add a `ModelProber` interface alongside the adapters (not in `port.Provider` — that interface is the seam to the framework core and must stay minimal):

```go
// ModelProber is an optional capability adapters may expose to validate
// their model configuration before accepting tasks.
type ModelProber interface {
    ProbeModel(ctx context.Context) error
}
```

Each adapter implements `ProbeModel` by spawning the CLI with a minimal canary prompt and a short deadline (10–15 s). `materializeAgents` uses a runtime type assertion after construction:

```go
if p, ok := prov.(claudecode.ModelProber); ok {
    if err := p.ProbeModel(ctx); err != nil {
        return nil, fmt.Errorf("wire: model probe for agent %q: %w", agCfg.Name, err)
    }
}
```

This is combined with fixing stderr capture so the probe (and RunTask) surfaces the real Claude error message.

- **Pros**: Fails fast at startup before HTTP servers start; actual model is validated against the API (catches non-existent models AND access restrictions); real error message surfaced to operator; clean — does not bloat `port.Provider` interface; fake binary can be extended to support `"badmodel"` sentinel to test the error path without a real CLI
- **Cons**: Makes one real (cheap) API call per agent at startup; requires network at startup; slightly increases startup latency (10–15 s worst case on slow networks, or immediately on model error)
- **Effort**: Medium

---

### 4. Hybrid lite — stderr capture + static format check + probe only on flag

Capture stderr always, add a basic format check (model must be non-empty, not contain spaces), and add a `--validate-models` flag to opt into the live probe at startup.

- **Pros**: No mandatory startup API call; operator chooses
- **Cons**: More surface area; the flag defeats "fail fast by default" goal; format check catches typos, not wrong model names
- **Effort**: Medium

---

## Recommendation

**Approach 3**, implemented as two separate tasks:

**Task A — Bugfix (critical, independent):** Capture stderr in both adapters and include it in error wrapping when the subprocess exits non-zero. This is a pure bugfix, no interface changes, and should ship regardless of the validation strategy chosen.

**Task B — Feature (startup validation):** Add `ModelProber` interface + `ProbeModel` implementation on both adapters. Call from `materializeAgents` with a 15-second context deadline. Extend `fakeclaude` with a `"badmodel"` sentinel (prints model error to stderr, exits 1) to keep tests fast and offline.

The split matters: Task A is a low-risk bugfix. Task B adds a startup API call and requires care in testing. Keeping them separate allows Task A to ship immediately.

---

## Risks

- **Startup latency**: `ProbeModel` makes a real API call. On a slow connection or under Anthropic rate limits this can delay startup by up to 15 seconds. Mitigation: the context deadline kills the probe; the error message tells the operator whether it was a model error or a timeout.
- **Token cost at startup**: A canary prompt (e.g., `"ping"`) on a valid model will consume a small number of tokens on every process start. Mitigation: use a deliberately minimal prompt; document the tradeoff.
- **opencode model validation behavior**: Not confirmed whether the opencode CLI has equivalent model validation semantics. The `ProbeModel` implementation for opencode should follow the same pattern but may need separate investigation if opencode does not exit 1 for invalid models.
- **`port.Provider` interface pollution risk**: Must NOT add `ProbeModel` to `port.Provider` — that interface is the core seam and is implemented by the fake provider in tests. A separate `ModelProber` interface avoids this.
- **Test isolation**: Probing in `materializeAgents` makes `cmd_test.go` and `e2e_test.go` dependent on a real CLI unless tests use `providerOverride`. Existing `wireOptions.providerOverride` already bypasses adapter construction — this protects existing tests.

---

## Ready for Proposal

**Yes.** The problem is well-understood, the affected files are identified, and the two-task split is clear. The proposal should communicate:

1. Task A (stderr capture) is a standalone bugfix that ships first.
2. Task B (startup probe) is the fail-fast feature that depends on Task A's stderr capture to surface meaningful errors.
3. The `ModelProber` interface lives in the adapter packages, not in `core/port`, to preserve the hexagonal architecture boundary.
