# Proposal: Fix Critical Gaps

## Intent

Three spec requirements exist in code but are silently broken: SSE push never
fires, gateway sends wrong message text, and INPUT_REQUIRED tasks restart
instead of resume after a server restart. All three are audited spec violations
with clear, minimal fixes — no new features, no design ambiguity.

## Scope

### In Scope
- Bug 2: `executeAction` sends literal `actionKind` string instead of actual body
- Bug 3: `RecoverOpenTasks` skips INPUT_REQUIRED tasks, causing silent re-execution on restart
- Bug 1: `UIHandler.Broadcast` is never called — SSE clients see only keep-alive pings
- Unit tests for each fix (policy, filter, broadcast)

### Out of Scope
- Adapter updates to populate `ActionIntent.Payload` (separate concern)
- Multi-agent SSE (only CEO supervisor hooked in v1)
- Parameterizing the hardcoded `Channel: "telegram"` in `executeAction`
- New features or capability additions

## Capabilities

### New Capabilities
None

### Modified Capabilities
- `monitoring-ui`: SSE push now fires on supervisor state + task state changes (Req 4)
- `a2a-transport`: SSE push wired via supervisor callback (Req 5)
- `approval-flow`: INPUT_REQUIRED tasks correctly seeded into a2asrv on restart (Req 2)

## Approach

Fix order: **Bug 2 → Bug 3 → Bug 1** (each self-contained, ascending blast radius).

- **Bug 2** — Add `PendingIntentBody string` to `TaskRecord`. Change `executeAction`
  to accept `body string`. Extract `body` from `intent.Payload["body"]` in permit
  path; pass `rec.PendingIntentBody` in resume path.
- **Bug 3** — Split `filterOpenTasks` → `filterWorkingTasks` + `filterInputRequiredTasks`.
  Add `registerFn func(ctx, taskID, input string) error` to `RecoverOpenTasks`. For
  INPUT_REQUIRED tasks call `registerFn` (closure over `store.Create` in `server.go`)
  instead of `handler.SendMessage`. Ignore `ErrTaskAlreadyExists`.
- **Bug 1** — Add `OnStateChange func()` to `supervisor.Config` + `SetOnStateChange`
  setter. Call hook (nil-safe) after FSM transitions and each `Store.Save`. In
  `main.go`, set `sup.SetOnStateChange(func() { ceoHandler.Broadcast(sup.Status()) })`.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `core/supervisor/supervisor.go` | Modified | Bug 2 body extraction; Bug 3 recover split; Bug 1 hook calls |
| `core/supervisor/store.go` | Modified | Add `PendingIntentBody string` to `TaskRecord` |
| `core/supervisor/state.go` | Modified | FSM transition sites — call notify after lock release |
| `transport/a2a/server.go` | Modified | Pass `registerFn` closure to `RecoverOpenTasks` |
| `cmd/company/main.go` | Modified | Wire `SetOnStateChange` after both objects constructed |
| `core/supervisor/supervisor_test.go` | Modified | Update filter test name/assertion for split behavior |
| `core/supervisor/policy_test.go` | New test | Assert gateway body ≠ `"telegram_send"` |
| `ui/handler_test.go` | New test | Assert `Broadcast` delivers SSE event to connected client |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Lock held when `OnStateChange` fires | Med | Call hook outside FSM lock (supervisor.go, not state.go methods) |
| Old TaskRecord files missing `PendingIntentBody` | Low | `omitempty` JSON tag; resume path uses empty string (same as current behavior) |
| `store.Create` user mismatch on INPUT_REQUIRED seed | Low | Authenticator always returns `"supervisor"` — same user context confirmed |
| Double-recovery calls `store.Create` twice | Low | Ignore `ErrTaskAlreadyExists` as idempotent success |
| Adapters don't populate `Payload["body"]` | Med | Bug 2 fix is necessary but not sufficient; adapter updates tracked separately |

## Rollback Plan

All changes are in `fix-critical-gaps` worktree. If any fix causes regression:
1. Revert the individual commit (each bug is a separate commit).
2. For Bug 2: restore original `executeAction(ctx, kind, token)` signature and remove `PendingIntentBody` from `TaskRecord`.
3. For Bug 3: revert `filterOpenTasks` to original single function; remove `registerFn` param.
4. For Bug 1: remove `OnStateChange` from `Config`; remove `SetOnStateChange`; remove hook call-sites.

No database migrations — file store is append-compatible JSON; removed fields are ignored on read.

## Dependencies

- `taskstore.InMemory.Create` from a2a SDK v2.5.0 (already in `go.mod`)

## Success Criteria

- [ ] `go test ./core/supervisor/...` passes with new policy and filter tests green
- [ ] `go test ./ui/...` passes with new `Broadcast` SSE delivery test
- [ ] Manual: approve a task after server restart — task resumes, does not restart
- [ ] Manual: `telegram_send` action delivers non-empty body to gateway
- [ ] Manual: frontend EventSource receives SSE event within 1 s of task state change
