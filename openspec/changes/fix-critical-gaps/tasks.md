# Tasks: Fix Critical Gaps

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 280–320 |
| 400-line budget risk | Low |
| Chained PRs recommended | No |
| Suggested split | Single PR |
| Delivery strategy | single-pr |
| Chain strategy | N/A |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: pending
400-line budget risk: Low

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | All three bug fixes + tests | PR 1 | `go test ./core/supervisor/... ./ui/...` | `go run ./cmd/company` → approve task after restart | Three independent commits; any single commit revertable without breaking others |

## Phase 1: Bug 2 — Gateway Body Propagation

- [x] 1.1 (RED) Add failing test in `core/supervisor/policy_test.go`: fake gateway with `LastCall()`, permit a `telegram_send` intent with `Payload["body"]="Hello from CEO"`, assert `Body != "telegram_send"`
- [x] 1.2 Add `PendingIntentBody string \`json:"pending_intent_body,omitempty"\`` field to `TaskRecord` in `core/supervisor/store.go`
- [x] 1.3 Add `extractBody(intent port.ActionIntent) string` in `core/supervisor/supervisor.go`; reads `intent.Payload["body"].(string)`, returns `""` if absent or wrong type
- [x] 1.4 Change `executeAction` to `(ctx, actionKind, body, token string)` in `core/supervisor/supervisor.go`; pass `body` to `OutboundMessage.Body`
- [x] 1.5 In `core/supervisor/supervisor.go` escalate path: `rec.PendingIntentBody = extractBody(intent)`; permit path: pass `extractBody(intent)` to `executeAction`; resume path: pass `rec.PendingIntentBody`
- [x] 1.6 (GREEN) `go test ./core/supervisor/...` — policy test must pass

## Phase 2: Bug 3 — INPUT_REQUIRED Recovery

- [x] 2.1 (RED) Add failing table test in `core/supervisor/supervisor_test.go` for `filterInputRequiredTasks`: mixed-state records, assert only `INPUT_REQUIRED` returned
- [x] 2.2 Rename `filterOpenTasks` → `filterWorkingTasks` in `core/supervisor/supervisor.go`; update existing test name; confirm it still excludes `INPUT_REQUIRED`
- [x] 2.3 Add `filterInputRequiredTasks(records []TaskRecord) []TaskRecord` in `core/supervisor/supervisor.go`; returns only `INPUT_REQUIRED` records
- [x] 2.4 Change `RecoverOpenTasks` in `core/supervisor/supervisor.go` to accept `registerFn func(ctx context.Context, taskID, input string) error`; call `registerFn` for each INPUT_REQUIRED record, treat `ErrTaskAlreadyExists` as success
- [x] 2.5 Add `registerFn` closure in `transport/a2a/server.go`: builds minimal `*sdka2a.Task{State: INPUT_REQUIRED}`, calls `store.Create`, ignores `ErrTaskAlreadyExists`; pass to `sup.RecoverOpenTasks`
- [x] 2.6 (GREEN) `go test ./core/supervisor/... ./transport/...` — filter tests must pass

## Phase 3: Bug 1 — SSE State Push Wiring

- [x] 3.1 (RED) Add failing test in `ui/handler_test.go`: connect a fake SSE client, call `handler.Broadcast(data)`, assert the client channel receives a line containing `"event: state"`
- [x] 3.2 Add `OnStateChange func()` to `supervisor.Config` in `core/supervisor/supervisor.go`; add `SetOnStateChange(fn func())` method and `notify()` helper with nil-guard
- [x] 3.3 Call `s.notify()` in `core/supervisor/supervisor.go` after: `fsm.ready()`, `fsm.taskStarted()`, `fsm.taskDone()` (deferred), `fsm.drain()`, and each `s.cfg.Store.Save(...)` — never inside `state.go` FSM methods
- [x] 3.4 In `cmd/company/main.go`, after supervisor and `ceoHandler` are both constructed: `runtimes[0].sup.SetOnStateChange(func() { ceoHandler.Broadcast(runtimes[0].sup.Status()) })`
- [x] 3.5 (GREEN) `go test ./ui/...` — Broadcast SSE delivery test must pass

## Phase 4: Integration Verification

- [x] 4.1 Add `core/supervisor/integration_test.go`: park task as INPUT_REQUIRED, re-wire a new handler on same file store, call `RecoverOpenTasks`, send approval message, assert `executeResume` runs (not fresh Execute)
- [x] 4.2 Run `go test ./...` — full suite green, no regressions
