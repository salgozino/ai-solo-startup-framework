```yaml
schema: gentle-ai.verify-result/v1
evidence_revision: sha256:8a8301c3596c3aadb15a1df7fd216c6bcfc98ae8c08bb0c2e8ed94110ad08595
verdict: fail
blockers: 2
critical_findings: 2
requirements: 2/4
scenarios: 10/13
test_command: go test ./... -v
test_exit_code: 0
test_output_hash: sha256:8a8301c3596c3aadb15a1df7fd216c6bcfc98ae8c08bb0c2e8ed94110ad08595
build_command: go build ./...
build_exit_code: 0
build_output_hash: sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

## Verification Report

**Change**: `fix-critical-gaps`
**Mode**: Standard (Strict TDD: inactive)
**Persistence**: both (OpenSpec + Engram)
**Date**: 2026-09-07

---

## Completeness

| Metric | Value |
|--------|-------|
| Tasks total | 15 |
| Tasks complete | 15 |
| Tasks incomplete | 0 |
| Delta specs | 3 (monitoring-ui, a2a-transport, approval-flow) |
| Requirements | 4 |
| Scenarios | 13 |

All tasks checked `[x]`. Full artifact set present (proposal, specs, design, tasks, exploration).

---

## Build & Tests Execution

**Build**: ✅ Passed
```text
go build ./...
exit 0 — no output (clean build)
hash: sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

**Static Analysis**: ✅ Passed
```text
go vet ./...
exit 0 — no warnings
```

**Tests**: ✅ 55+ passed / ❌ 0 failed / ⚠️ 1 skipped (live Telegram integration, env not set)
```text
go test ./... -v
exit 0
hash: sha256:8a8301c3596c3aadb15a1df7fd216c6bcfc98ae8c08bb0c2e8ed94110ad08595

Packages:
ok  github.com/salgozino/ai-solo-startup-framework/adapters/claudecode
ok  github.com/salgozino/ai-solo-startup-framework/adapters/opencode
ok  github.com/salgozino/ai-solo-startup-framework/cmd/company
ok  github.com/salgozino/ai-solo-startup-framework/config
ok  github.com/salgozino/ai-solo-startup-framework/core/address
ok  github.com/salgozino/ai-solo-startup-framework/core/policy
ok  github.com/salgozino/ai-solo-startup-framework/core/port
ok  github.com/salgozino/ai-solo-startup-framework/core/supervisor
ok  github.com/salgozino/ai-solo-startup-framework/gateways/telegram
ok  github.com/salgozino/ai-solo-startup-framework/transport/a2a
ok  github.com/salgozino/ai-solo-startup-framework/ui

New tests added (all pass):
  TestSupervisor_BodyPropagatedToGateway              — permit path body ≠ "telegram_send" ✅
  TestFilterWorkingTasks                              — filterWorkingTasks excludes INPUT_REQUIRED ✅
  TestFilterInputRequiredTasks                        — returns only INPUT_REQUIRED ✅
  TestIntegration_InputRequiredRecoveredAndResumed    — recovery + approval → resume (not re-run) ✅
  TestSSEReceivesEvent                                — SSE client receives Broadcast event ✅
  TestSSEBroadcastEventType                           — Broadcast delivers "event: state" line ✅
  TestSupervisor_RestartPreservesInputRequired        — INPUT_REQUIRED survives restart in store ✅
  TestSupervisor_EscalationCycle                      — full escalation + approval cycle ✅
```

**Coverage**: Not measured — standard mode, no coverage threshold configured.

---

## Spec Compliance Matrix

### monitoring-ui — "State Updates Reach the UI Without Polling"

| Requirement | Scenario | Test | Result |
|-------------|----------|------|--------|
| State Updates Reach UI | An escalation appears live without a manual refresh | `ui/handler_test.go > TestSSEReceivesEvent` | ✅ COMPLIANT |
| State Updates Reach UI | State change triggers an SSE event | `ui/handler_test.go > TestSSEReceivesEvent, TestSSEBroadcastEventType` | ✅ COMPLIANT |
| State Updates Reach UI | A single transition emits exactly one broadcast | No dedicated multi-client count test — `notify()` is called at discrete sites but "exactly one Broadcast per transition" is not runtime-proven | ⚠️ PARTIAL |
| State Updates Reach UI | Nil callback does not crash the supervisor | Implicit: all supervisor tests run without `OnStateChange` set and pass; `notify()` nil-guard at `supervisor.go:93–97` | ✅ COMPLIANT |

**Requirement verdict**: PASS WITH WARNINGS (3/4 scenarios runtime-evidenced)

### a2a-transport — "Push Notifications Stream Task State in Real Time"

| Requirement | Scenario | Test | Result |
|-------------|----------|------|--------|
| Push Notifications Stream | A state transition appears on the stream without polling | `ui/handler_test.go > TestSSEReceivesEvent` | ✅ COMPLIANT |
| Push Notifications Stream | Callback registered after both objects are constructed | `go build ./...` exit 0; `cmd/company/main.go:72–73` confirms `SetOnStateChange` called post-construction; SSE tests confirm mechanism works | ✅ COMPLIANT |

**Requirement verdict**: COMPLIANT (2/2 scenarios evidenced)

### approval-flow — "Gateway Action Body Carries the Actual Message Text"

| Requirement | Scenario | Test | Result |
|-------------|----------|------|--------|
| Gateway Body | Permit path sends the actual message body | `core/supervisor/policy_test.go > TestSupervisor_BodyPropagatedToGateway` — asserts `Body == "Hello from CEO"` on permit path | ✅ COMPLIANT |
| Gateway Body | **Resume path sends the persisted body** | **None** — no test escalates with `Payload["body"]="Hello from CEO"`, approves, and asserts gateway `Body == "Hello from CEO"` on the resume path. `TestSupervisor_EscalationCycle` exercises the resume path but with empty body only. | ❌ UNTESTED |
| Gateway Body | Missing payload body defaults to empty string | `extractBody` nil/absent path implicitly covered by `TestSupervisor_EscalationCycle` (intent has no Payload → body = "") | ✅ COMPLIANT |

**Requirement verdict**: FAIL (1/3 scenarios fully evidenced; 1 CRITICAL UNTESTED)

### approval-flow — "An Escalated Task Survives a Process Restart"

| Requirement | Scenario | Test | Result |
|-------------|----------|------|--------|
| Task Survives Restart | Escalated task is still resumable after a restart | `core/supervisor/policy_test.go > TestSupervisor_RestartPreservesInputRequired` — store retains INPUT_REQUIRED after restart | ✅ COMPLIANT |
| Task Survives Restart | INPUT_REQUIRED task is seeded into a2asrv on recovery | `core/supervisor/integration_test.go > TestIntegration_InputRequiredRecoveredAndResumed` — registerFn called, task seeded, approval routes to resume | ✅ COMPLIANT |
| Task Survives Restart | Approval after restart resumes the task, not restarts it | `core/supervisor/integration_test.go > TestIntegration_InputRequiredRecoveredAndResumed` — `RunTaskCallCount` unchanged after approval | ✅ COMPLIANT |
| Task Survives Restart | **Double-recovery is idempotent** | **None** — no test calls `RecoverOpenTasks` (or `transa2a.New`) twice for the same INPUT_REQUIRED task to exercise `ErrTaskAlreadyExists` suppression at `server.go:102–104` | ❌ UNTESTED |

**Requirement verdict**: FAIL (3/4 scenarios evidenced; 1 CRITICAL UNTESTED)

**Compliance summary**: 10/13 scenarios compliant

---

## Correctness (Static Evidence)

| Requirement | Status | Notes |
|-------------|--------|-------|
| `PendingIntentBody` in `TaskRecord` | ✅ Implemented | `store.go:33` — `omitempty`, backward-compatible |
| `extractBody` reads `Payload["body"]` | ✅ Implemented | `supervisor.go:524–534` — nil-safe, type-asserted |
| `executeAction` passes body to gateway | ✅ Implemented | `supervisor.go:448–459` — `Body: body` |
| `OnStateChange func()` in `Config` | ✅ Implemented | `supervisor.go:49` |
| `SetOnStateChange` method | ✅ Implemented | `supervisor.go:87–89` |
| `notify()` nil-safe helper | ✅ Implemented | `supervisor.go:93–97` |
| `notify()` called outside FSM lock | ✅ Implemented | All call sites in `supervisor.go`; none in `state.go` |
| `filterWorkingTasks` excludes INPUT_REQUIRED | ✅ Implemented | `supervisor.go:538–547` |
| `filterInputRequiredTasks` returns only INPUT_REQUIRED | ✅ Implemented | `supervisor.go:551–559` |
| `RecoverOpenTasks` accepts `registerFn` | ✅ Implemented | `supervisor.go:112–157` |
| `registerFn` treats `ErrTaskAlreadyExists` as success | ✅ Implemented | `server.go:102–104` |
| `main.go` wires `SetOnStateChange` after both objects | ✅ Implemented | `main.go:72–73` — post-construction |

---

## Coherence (Design)

| Decision | Followed? | Notes |
|----------|-----------|-------|
| `PendingIntentBody string` with `omitempty` | ✅ Yes | Exact field as designed |
| `registerFn` injection (not TaskStore exposure) | ✅ Yes | One param, supervisor stays store-agnostic |
| `OnStateChange func()` in Config + `SetOnStateChange` | ✅ Yes | Decoupled, zero new deps |
| `notify()` in `supervisor.go` only, never in `state.go` | ✅ Yes | Lock-order hazard avoided |
| Bug fix order: Bug 2 → Bug 3 → Bug 1 | ✅ Yes | Separate commits, ascending blast radius |
| Files changed match design file list | ✅ Yes | 7 files changed match design table exactly |
| No `core/` → `ui/` or `transport/` imports | ✅ Yes | `go build ./...` passes, no import violations |

---

## Issues Found

### CRITICAL

**[C1] UNTESTED: "Resume path sends the persisted body"** (approval-flow / Gateway Body Requirement, Scenario 2)

The implementation at `supervisor.go:298` correctly passes `rec.PendingIntentBody` to `executeAction` on the resume path. However, no test verifies this with a non-empty body:

- `TestSupervisor_BodyPropagatedToGateway` uses `risk: "low"` (Permit path) — never goes through escalate → resume.
- `TestSupervisor_EscalationCycle` exercises escalate → approve but the intent has no `Payload`, so `PendingIntentBody = ""` and the gateway body assertion never fires for a non-empty value.
- `TestIntegration_InputRequiredRecoveredAndResumed` seeds the store with no `PendingIntentBody` field.

**Required fix**: add a test that sets `risk: "risky"` + `ActionIntent.Payload["body"] = "Hello from CEO"`, lets the task escalate, approves it, and asserts `gw.LastCall().Body == "Hello from CEO"`.

**[C2] UNTESTED: "Double-recovery is idempotent"** (approval-flow / Task Survives Restart Requirement, Scenario 4)

`server.go:102–104` correctly ignores `ErrTaskAlreadyExists` in `registerFn`. No test exercises this path:

- `TestIntegration_InputRequiredRecoveredAndResumed` calls `transa2a.New` once with a pre-seeded store.
- No test calls `transa2a.New` (or `sup.RecoverOpenTasks` directly) twice with the same INPUT_REQUIRED task to trigger `ErrTaskAlreadyExists` in `store.Create`.

**Required fix**: add a test that creates two `transa2a.Server` instances (or calls `RecoverOpenTasks` twice) with the same file store containing an INPUT_REQUIRED task, and asserts no error on the second call.

### WARNING

**[W1] PARTIAL: "A single transition emits exactly one broadcast"** (monitoring-ui, Scenario 3)

`notify()` is called at discrete, non-looping sites in `supervisor.go`, so exactly one `OnStateChange` invocation fires per FSM event. However, no test connects two SSE clients and verifies `Broadcast` is called exactly once (not per-client). Current tests call `Broadcast` directly, not via the `OnStateChange` callback path.

### SUGGESTION

**[S1]** `supervisor.go:143` and `supervisor.go:153`: recovery error paths use `_ = fmt.Errorf(...)`, silently discarding formatted errors. Replace with `s.cfg.Logger.Warn(...)` for observability. (No spec violation.)

---

## Regression Check

All 55+ pre-existing tests pass (`go test ./... exit 0`). No regressions.

---

## Verdict

**FAIL**

Two spec scenarios have no passing runtime covering test:

1. **[C1]** "Resume path sends the persisted body" — approval-flow Scenario 2. Implementation is correct but the test covers only the permit path; the resume path body is never asserted with a non-empty value.
2. **[C2]** "Double-recovery is idempotent" — approval-flow Scenario 4. Implementation is correct (`ErrTaskAlreadyExists` suppressed) but no test actually triggers this code path.

Both gaps require new test additions only — no implementation changes needed. Re-run verification after adding the two tests.
