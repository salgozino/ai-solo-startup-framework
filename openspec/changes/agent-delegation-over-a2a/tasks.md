# Tasks: Agent Delegation Over A2A

> **Scope note**: this change ships design slices **1–6 and 10 only** (~1670 estimated authored
> changed lines across 7 PRs). Design slices 7 (chained approval), 8 (restart recovery), and 9
> (UI aggregation) are **DEFERRED** to the follow-up change `agent-delegation-chained-approval`
> and MUST NOT be implemented here. No task below creates `AwaitingPeerRole`,
> `AwaitingPeerTaskID`, `AwaitingPeerDeadline`, `TaskResumer`, `SetResumer`, a watcher goroutine,
> `companyUIAdapter`, or any UI badge for awaiting-peer state. The single interim requirement this
> change DOES carry — a non-terminal peer result FAILS the delegating task with an explicit
> not-yet-wired error — is implemented in Phase 6 and MUST NOT be expanded into chained approval.

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~1670 authored lines across 7 PR slices (range 60–380 per slice) |
| 400-line budget risk | High (aggregate); each individual slice is designed to land ≤ 400 lines |
| Chained PRs recommended | Yes |
| Suggested split | PR 1 (Phase 1) → PR 2 (Phase 2) → PR 3 (Phase 3) → PR 4 (Phase 4) → PR 5 (Phase 5) → PR 6 (Phase 6) → PR 7 (Phase 7) |
| Delivery strategy | auto-chain |
| Chain strategy | pending — orchestrator selects `stacked-to-main` or `feature-branch-chain` after this forecast; either is workable because slices 1→2→3→4→5→6→7 have a strict linear dependency order (each slice is a prerequisite for the next, per the design's "Independently green because" column) |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain (maintainer decision, cached 2026-09-18 for this session)
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | Narrow `port.Provider`, delete the six adapter stubs, `fake.Provider`'s A2A surface, `executeDelegation`, `roleOf`, and the `PolicyEngine == nil` branch. Zero production behavior change (branch was already unreachable). | PR 1 | `go test ./core/port/... ./adapters/... ./core/supervisor/... -run TestProvider` | N/A — pure deletion of unreachable code; no new user-observable behavior exists yet to demonstrate | Revert PR 1 only: restores the six stubs and the dead branch; no other slice depends on the deleted code still existing |
| 2 | `supervisor.New` returns `(*Supervisor, error)`, erroring on nil `Config.PolicyEngine`. | PR 2 | `go test ./core/supervisor/... -run TestNew` | N/A — construction-time contract change with no new runtime path; existing `cmd/company/wire.go` call sites are updated in the same PR so `go build ./...` remains green | Revert PR 2 only: restores single-return `New`; PR 1's deletions are independent and stay reverted-or-not on their own |
| 3 | `PeerDirectory` (role → live base URL), wired into `materializeAgents` with duplicate-role and not-yet-bound detection. Nothing consumes it yet. | PR 3 | `go test ./transport/a2a/... -run TestPeerDirectory -race` | `go build ./cmd/company && go run ./cmd/company materialize company.yaml` — starts normally; directory is populated but unused, so behavior is unchanged (observe via added debug log, removed before merge, or rely on the unit test) | Revert PR 3 only: removes `directory.go`/`directory_test.go` and the `Bind` calls in `wire.go`; delegation still does not exist |
| 4 | `core/port/delegator.go` (`Delegator`, `DelegationResult`, `KindDelegateTask`, `TargetArg`) + `transport/a2a/client.go` implementing it; `go.mod` gains `golang.org/x/mod` in this same commit. Not injected into any supervisor yet. **Largest slice — if it approaches 400 lines, split `Delegate` (its own commit/PR) from `PeerTaskState` (follow-on commit/PR) as pre-planned in the design.** | PR 4 | `go test ./transport/a2a/... -run TestClient -race` | `go test ./transport/a2a/... -run TestClient_ -v` against two real `transa2a.Server` instances on loopback (integration tests already exercise the real wire; no CLI needed) | Revert PR 4 only: removes the client and the port; `go.mod` entry can stay (harmless unused indirect require) or revert with it |
| 5 | Terminal `COMPLETED` event carries `rec.Output` as a text part. Tiny but load-bearing for PR 6. | PR 5 | `go test ./core/supervisor/... -run TestExecuteWithPolicy_Telegram` | `go run ./cmd/company materialize company.yaml` then complete any Permitted action; observe `Status.Message` populated on the terminal A2A event via a real `SendMessage` round trip in the integration test | Revert PR 5 only: `COMPLETED` events go back to a nil message; PR 6 has not been merged yet so nothing regresses |
| 6 | `Config.Delegator`, `Config.DelegateTimeout`, `actionOutcome`, `executeAction` routing `delegate_task` to the port, terminal/failed/timeout/unknown-role handling, the explicit not-yet-wired error for a non-terminal peer, `fake.Delegator`. | PR 6 | `go test ./core/supervisor/... -run TestExecuteAction -race` | N/A — tests declare their own `PolicyConfig`; the shipped `company.yaml` still has no `delegate_task` entry, so no live agent can reach this path yet | Revert PR 6 only: removes delegation routing; PR 3/4/5 stay in the tree as unused plumbing |
| 7 | Turn it on: `company.yaml` declares `delegate_task` (`allowed_roles: [ceo]`, `risk: safe`), `agents/ceo.md` delegation instructions, `agents/engineer.md` minimal peer persona, final `wire.go` injection (`Client` construction, `Config.Delegator`, `DelegateTimeout`, handling `supervisor.New`'s error). | PR 7 | `go test ./... -race` (full suite) | `go run ./cmd/company materialize company.yaml` with two real agents and a real CEO delegation tool call — the Success Criteria scenario from the proposal | Revert PR 7 only (config-level rollback): removing the `delegate_task` policy entry alone disables delegation; the policy engine fails closed on the undeclared kind |

## Phase 1: Narrow `port.Provider` — Delete Dead Delegation Code (PR 1)

- [x] 1.1 RED: Update `core/port/contract_test.go` — drop the A2A-method assertions and add a
      reflection-based test (`TestProvider_MethodSet`) asserting `port.Provider`'s method set is
      exactly `Complete, CompleteError, SendTask, RunTask, Capabilities`. Run it first; it MUST
      fail against the current five-method-plus-three interface.
- [x] 1.2 GREEN: Edit `core/port/provider.go` — delete `SendMessage`, `SendMessageStream`,
      `ResolveAgent` from the `Provider` interface and delete the `StreamEvent` struct; update the
      interface's doc comment to state the narrowed contract (Complete/CompleteError/SendTask/
      RunTask/Capabilities only, no A2A networking).
- [x] 1.3 GREEN: Edit `core/port/fake/fake_provider.go` — delete `SendMessage`,
      `SendMessageStream`, `ResolveAgent`, and the now-unused `SendMessageCall`, `MsgCalls`,
      `ReturnStream`, `ReturnAddress`, `SendMessageCallCount` fields/methods; keep `SendTask`.
      Re-run 1.1's test; it must now pass.
- [x] 1.4 GREEN: Edit `adapters/claudecode/adapter.go` — delete the `SendMessage`,
      `SendMessageStream`, `ResolveAgent` stub methods and the false
      `errNotImplemented`/"provided by transport/a2a" comment (lines ~517-541); keep the
      `errNotImplemented` variable only if still referenced by `Complete`/`CompleteError` stubs,
      otherwise delete it too.
- [x] 1.5 GREEN: Edit `adapters/opencode/adapter.go` — same three-method deletion as 1.4 (lines
      ~513-526).
- [x] 1.6 RED: In `transport/a2a/server_test.go`, write a new/retargeted
      `TestProviderFailureMarksFailed` that exercises the `RunTask` failure path (the behavior it
      actually proves per the proposal), removing any assertion coupled to the deleted
      `PolicyEngine == nil` branch. Confirm it fails for the right reason before Phase 1's
      supervisor edit (it should currently pass against old code but assert on the wrong branch —
      capture the diff in the commit message).
- [x] 1.7 GREEN: Edit `core/supervisor/supervisor.go` — delete `executeDelegation` (lines
      ~413-426 area) and `roleOf` (lines ~578-582), and delete the `PolicyEngine == nil` branch
      that dispatched to `executeDelegation`. `wire.go:351` already always sets `PolicyEngine`, so
      this removes unreachable code only; confirm no remaining caller references either deleted
      function (`go build ./...`).
- [x] 1.8 Run `go build ./... && go vet ./...` and the full `core/supervisor`, `core/port`,
      `adapters/claudecode`, `adapters/opencode`, `transport/a2a` package tests. Confirm `go test
      ./... -race` stays green outside the areas intentionally changed in this phase.

## Phase 2: `supervisor.New` Returns a Construction Error (PR 2)

- [x] 2.1 RED: In `core/supervisor/supervisor_test.go`, add
      `TestNew_NilPolicyEngineReturnsError` asserting `New(Config{PolicyEngine: nil, ...})` returns
      a non-nil error and a nil `*Supervisor`, and `TestNew_ValidConfigSucceeds` asserting a
      non-nil `Config.PolicyEngine` constructs successfully and the resulting supervisor
      progresses to `IDLE` via `MarkReady()`. Run and confirm both fail against the current
      single-return `New`.
- [x] 2.2 GREEN: Edit `core/supervisor/supervisor.go` — change `New(cfg Config) *Supervisor` to
      `New(cfg Config) (*Supervisor, error)`, returning an explicit error when
      `cfg.PolicyEngine == nil` before constructing the FSM. Update the doc comment: "there is no
      supported delegation-only, no-policy construction mode."
- [x] 2.3 GREEN: Update every `supervisor.New(...)` call site to handle the new error return:
      `cmd/company/wire.go` (all 11 call-site references reported by the current blast radius),
      `core/supervisor/policy_test.go`, `core/supervisor/integration_test.go`,
      `transport/a2a/server_test.go`. Each production call site in `wire.go` must propagate the
      error out of `materializeAgents` rather than panicking or ignoring it.
- [x] 2.4 Run `go test ./core/supervisor/... ./cmd/company/... -race` and confirm 2.1's tests pass
      and no existing test broke from the signature change. Run `go build ./...`.

## Phase 3: Peer Directory (PR 3)

- [x] 3.1 RED: Create `transport/a2a/directory_test.go` with table-driven tests:
      `TestPeerDirectory_UnknownRole` (query an undeclared role → `ErrUnknownRole`, error names
      the role, no panic), `TestPeerDirectory_NotYetBound` (query a declared-but-unbound role →
      `ErrPeerNotRegistered`, error names the role, no panic), `TestPeerDirectory_BoundRoleReturnsURL`,
      `TestNewPeerDirectory_DuplicateRoleFails` (constructing with a duplicate role in the input
      list returns an error). Add a concurrency case running `Bind` and `BaseURL` from parallel
      goroutines under `go test -race`. Confirm all fail (package does not exist yet).
- [x] 3.2 GREEN: Create `transport/a2a/directory.go` — `PeerDirectory` struct (`declared map`,
      `bound map`, `sync.RWMutex`), `ErrUnknownRole`, `ErrPeerNotRegistered`,
      `NewPeerDirectory(roles []string) (*PeerDirectory, error)`,
      `Bind(role, baseURL string) error`, `BaseURL(role string) (string, error)` per design D7.
      Run 3.1's tests; confirm they pass, including under `-race`.
- [x] 3.3 RED: In `cmd/company/wire_test.go`, add
      `TestMaterializeAgents_DuplicateRoleFailsBeforeAnyReady` — two agents sharing a role in the
      test config must fail `materializeAgents` before any supervisor's `MarkReady()` is observed
      (assert via a recorded state-change hook or supervisor status). Confirm it fails against
      current `wire.go` (no directory construction exists).
- [x] 3.4 GREEN: Edit `cmd/company/wire.go` — call `NewPeerDirectory` with the full declared role
      list at the top of `materializeAgents`, before the per-agent loop (so a duplicate fails
      materialize before any `transa2a.New` runs); call `dir.Bind(role, srv.BaseURL())`
      immediately after each successful `transa2a.New(sup, ...)` in the loop. Confirm 3.3 passes.
- [x] 3.5 Also add `TestMaterializeAgents_DistinctRolesBindEachAgent` in `wire_test.go` asserting
      every agent's role resolves to its own bound base URL after materialize (spec:
      `company-as-code` — "Distinct roles across agents materialize normally").
- [x] 3.6 Run `go test ./transport/a2a/... ./cmd/company/... -race` and `go build ./...`.

## Phase 4: A2A Client (PR 4)

> **Split point if this phase approaches 400 changed lines**: land 4.1–4.5 (`Delegate` +
> card/auth/tenant plumbing) as PR 4a, then 4.6–4.8 (`PeerTaskState` + its tests) as PR 4b,
> re-targeting 4b onto 4a's branch per the chosen chain strategy. Do not shrink test coverage or
> comments to stay under budget — split the work instead.

- [x] 4.1 Update `go.mod` in this same commit: add `golang.org/x/mod v0.35.0 // indirect` (already
      present in `go.sum`; required transitively by `a2aclient/factory.go`). Run `go build ./...`
      to confirm the missing-requirement build failure that appears the moment `a2aclient` is
      imported (see 4.3) is resolved by this line, not masked.
- [x] 4.2 Create `core/port/delegator.go` — `KindDelegateTask = "delegate_task"` constant,
      `TargetArg = "target"` constant, `DelegationResult` struct (`PeerTaskID`, `State`, `Output`),
      `Delegator` interface with `Delegate(ctx, role, body string) (DelegationResult, error)` and
      `PeerTaskState(ctx, role, peerTaskID string) (DelegationResult, error)` per design D6. No
      `TaskResumer` in this file — that type belongs to the deferred follow-up change.
- [x] 4.3 RED: Create `transport/a2a/client_test.go` with real-server integration tests (gated on
      `testing.Short()` per repo convention), against a real `transa2a.Server` + `fake.Provider`:
      `TestClient_CardResolvedBeforeSend` (request-recording `http.RoundTripper` proves the
      well-known card was fetched before `/invoke`), `TestClient_BearerPresentAndAccepted`,
      `TestClient_SessionlessRequestIsRejectedByPeer` (omitting `AttachSessionID` → peer rejects,
      proving the silent-no-op auth-interceptor risk is closed),
      `TestClient_WrongTokenIsRejectedByPeer`, `TestClient_WrongTenantIsRejectedByPeer`,
      `TestClient_BlocksUntilPeerTerminal`, `TestClient_DeadlineReturnsDistinguishableTimeout`.
      Confirm all fail (package `Client` does not exist yet).
- [x] 4.4 GREEN: Create `transport/a2a/client.go` — `Client` struct (`dir *PeerDirectory`,
      `tenant string`, `creds *a2aclient.InMemoryCredentialsStore`, `session a2aclient.SessionID`,
      `resolver *agentcard.Resolver`) implementing `port.Delegator.Delegate`: resolve `base` via
      `dir.BaseURL(role)`, resolve the Agent Card via `resolver.Resolve(ctx, base)` BEFORE any send,
      construct the peer client via `a2aclient.NewFromCard(ctx, card,
      a2aclient.WithInterceptors(&a2aclient.AuthInterceptor{Service: c.creds}))`, call
      `a2aclient.AttachSessionID(ctx, c.session)` on every call (mandatory — omitting it sends an
      unauthenticated request with no error per design D8), then `peer.SendMessage(ctx,
      &a2a.SendMessageRequest{Tenant: c.tenant, Message: msg})`. Seed the credentials store at
      construction with `session → {a2a.SecuritySchemeName("bearer"): AuthCredential(authToken)}`
      — the literal string `"bearer"` must match `buildAgentCard`'s published scheme name
      (`transport/a2a/server.go:222-227`).
- [x] 4.5 Run 4.3's `Delegate`-covering tests; confirm they pass. Fix the stale comment at
      `core/supervisor/integration_test.go:88-92` that incorrectly claims the client cannot attach
      a Bearer header — delete or correct it in this commit, since `TestClient_BearerPresentAndAccepted`
      now proves it wrong.
- [x] 4.6 RED: Add `TestClient_PeerTaskStateReportsCurrentStatus` and
      `TestClient_PeerTaskStateReportsTerminalOutput` to `client_test.go`, covering
      `PeerTaskState`'s independent read path (used both to await a parked peer and, in the
      deferred follow-up, to re-establish a wait after restart — this change only needs the
      read itself). Confirm they fail.
- [x] 4.7 GREEN: Implement `Client.PeerTaskState` in `client.go` using `a2aclient.GetTask` against
      the resolved peer, reading `task.Status.Message` for output (this depends on Phase 5's
      output-on-the-wire change being present in the peer's supervisor for the terminal-output
      case — order Phase 5 before merging this task if testing against a peer running the new
      supervisor code; the client itself has no dependency on Phase 5's edit).
- [x] 4.8 Run `go test ./transport/a2a/... -race` (full package, integration tests included) and
      `go build ./...`.

## Phase 5: The Peer's Output Must Be Put on the Wire (PR 5)

- [x] 5.1 RED: In `core/supervisor/supervisor_test.go` (or a focused new test file), add
      `TestExecuteWithPolicy_TelegramSendCompletionCarriesOutput` (or equivalent covering the
      existing non-delegated `Permit` path) asserting that the terminal `COMPLETED`
      `a2a.StatusUpdateEvent` carries `rec.Output` as a text part in `Status.Message`, not nil.
      Confirm it fails against the current `NewStatusUpdateEvent(execCtx, COMPLETED, nil)` call.
- [x] 5.2 GREEN: Edit `core/supervisor/supervisor.go` — change the terminal `COMPLETED` event
      construction to attach `rec.Output` as a text part on the status message instead of `nil`.
      Confirm 5.1 passes.
- [x] 5.3 Add a regression assertion (can be part of 5.1's test or a sibling) confirming this
      change is additive: an existing non-delegated completed task still transitions correctly and
      no consumer of `Status.Message` on `COMPLETED` regresses (`FAILED` keeps using the existing
      generic `errorMessage(err)` — do not touch that path).
- [x] 5.4 Run `go test ./core/supervisor/... ./transport/a2a/... -race` and `go build ./...`.

## Phase 6: Supervisor Delegation Routing — Synchronous Half (PR 6)

- [x] 6.1 Create `core/port/fake/fake_delegator.go` — `fake.Delegator` implementing
      `port.Delegator` with per-role scripted `Delegate` results/errors and a sequenced
      `PeerTaskState` return list, recording every call (`Calls []DelegateCall` or equivalent).
      This is test infrastructure, not itself behavior under test — no RED/GREEN pair required,
      but confirm it compiles and satisfies `port.Delegator` (`var _ port.Delegator =
      (*Delegator)(nil)`).
- [x] 6.2 RED: In `core/supervisor/supervisor_test.go`, add
      `TestExecuteAction_DelegateTaskRoutesToPortNotGateway` — a `Permit`-classified
      `delegate_task` intent must call `fake.Delegator.Delegate` and `fake.Gateway.Send` must have
      zero calls. Add `TestExecuteAction_TelegramSendStillRoutesToGateway` — a `Permit`-classified
      `telegram_send` intent is unaffected, calling `Gateway.Send` exactly as before. Confirm the
      first test fails (no routing exists yet) and the second currently passes (guard against
      regressing it).
- [x] 6.3 GREEN: Edit `core/supervisor/supervisor.go` — add `Config.Delegator port.Delegator` and
      `Config.DelegateTimeout time.Duration` (zero-value defaults to 10 minutes); add the
      `actionOutcome` struct (`Output` field only in this change — no `AwaitingPeerRole` /
      `AwaitingPeerTaskID` fields, since chained approval is deferred); change `executeAction`'s
      signature from `error` to `(actionOutcome, error)`; add the `delegate_task` branch that
      calls `Config.Delegator.Delegate(ctx, role, body)` bounded by `DelegateTimeout`, and an
      `extractTarget` helper reading the intent payload's `target` key. Confirm 6.2 passes.
- [x] 6.4 RED: Add `TestExecuteAction_DelegationCompletesWithPeerOutput` — a `fake.Delegator`
      returning a terminal `COMPLETED` `DelegationResult{Output: "Done: X"}` must result in the
      delegating task's own output containing `"Done: X"` and transitioning to `COMPLETED`.
      Confirm it fails.
- [x] 6.5 GREEN: Wire the `COMPLETED` case through `executeAction`'s delegation branch — copy
      `DelegationResult.Output` into the task's output before marking `COMPLETED`. Confirm 6.4
      passes.
- [x] 6.6 RED: Add `TestExecuteAction_DelegationFailedPeerFailsDelegatingTask` — a `fake.Delegator`
      returning a `FAILED`-state `DelegationResult` (or an error) must fail the delegating task
      with an error naming the peer role, fabricating no output. Confirm it fails.
- [x] 6.7 GREEN: Wire the `FAILED` case. Confirm 6.6 passes.
- [x] 6.8 RED: Add `TestExecuteAction_DelegationTimeoutFailsTaskNamingPeerAndDuration` — inject a
      controllable `Config.Now` clock (add `Config.Now func() time.Time` if not already present;
      default `time.Now`) so no `time.Sleep` is needed; simulate the `Delegate` call exceeding
      `DelegateTimeout` and assert the delegating task reaches `FAILED` with an error naming the
      role and the configured duration. Confirm it fails.
- [x] 6.9 GREEN: Wire timeout handling using `context.WithTimeout(ctx, cfg.DelegateTimeout)`
      around the `Delegate` call. Confirm 6.8 passes.

      > Implementation note (6.8/6.9): no `Config.Now` clock was added. The timeout is
      > enforced by `context.WithTimeout` around `Delegate`; `fake.Delegator.BlockUntilCtxDone`
      > honors ctx cancellation, so the test drives the real mechanism with a 20ms
      > `DelegateTimeout` and no `time.Sleep`. An injected clock only serves the deferred
      > awaiting-peer watcher (`agent-delegation-chained-approval`) and would be dead code here.
- [x] 6.10 RED: Add `TestExecuteAction_DelegationUnknownRoleFailsTaskExplicitly` — a
      `fake.Delegator` returning `transport/a2a.ErrUnknownRole` (or an equivalent sentinel wired
      through the fake) must fail the delegating task with an explicit error naming the role,
      never a panic. Confirm it fails.
- [x] 6.11 GREEN: Wire the unknown/unregistered-role error path (do not distinguish
      `ErrUnknownRole` from `ErrPeerNotRegistered` beyond surfacing the returned error text — both
      are terminal failures for this synchronous-only change; only the deferred watcher needs to
      treat `ErrPeerNotRegistered` as transient). Confirm 6.10 passes.
- [x] 6.12 RED (honest-interim requirement — spec: agent-delegation "A Non-Terminal Peer Result
      Fails the Delegating Task With an Explicit Not-Yet-Wired Error"): add
      `TestExecuteAction_DelegationNonTerminalPeerFailsWithNotYetWiredError` — a `fake.Delegator`
      returning `DelegationResult{State: "TASK_STATE_INPUT_REQUIRED", PeerTaskID: "P"}` must fail
      the delegating task immediately (not waiting for `DelegateTimeout`) with an error that (a)
      states the peer escalated, (b) states that chained approval is not yet wired, and (c) names
      both the peer role and peer task ID `"P"`. Also assert the task never reaches `COMPLETED` and
      no output is fabricated. Confirm it fails.
- [x] 6.13 GREEN: In `executeAction`'s delegation branch, use
      `a2a.TaskState(res.State).Terminal()` (already available via `core/supervisor`'s existing
      `a2a` import) to distinguish terminal from non-terminal; any non-terminal state — including,
      but not limited to, `INPUT_REQUIRED` — fails the task immediately with the composed
      not-yet-wired error message. Confirm 6.12 passes.
- [x] 6.14 RED: Add `TestDelegateTask_FromNonAllowedRoleIsHardDenied` in the policy/classification
      test path (`core/supervisor/policy_test.go` or `core/policy` tests, matching existing
      HardDeny coverage) — a `delegate_task` intent from a role not in `delegate_task`'s
      `allowed_roles` must be `HardDeny`-classified and drive the task to `REJECTED`, using the
      existing capability check with no new hop-counter or delegation-depth state. Also add a
      payload-shape assertion that the recorded intent's payload keys are exactly `{target,
      body}`. Confirm the HardDeny case passes against the unchanged policy engine (no engine
      code change is expected — this test documents that no new guard was added) and the
      payload-shape assertion is meaningful once 6.3's `extractTarget` exists.
- [x] 6.15 Run `go test ./core/supervisor/... ./core/policy/... ./core/port/... -race` and `go
      build ./...`. Confirm `TestExecuteAction_DelegationCompletesWithPeerOutput` and friends do
      not regress `TestExecuteAction_TelegramSendStillRoutesToGateway`.

      > Known limitation (documented by `TestResume_DelegateTaskWithoutPersistedTargetFailsExplicitly`):
      > an escalated `delegate_task` cannot be resumed because `TaskRecord` persists only
      > `PendingIntentKind`/`PendingIntentBody` (task 8.3 forbids new fields). The resume path
      > fails such a task explicitly instead of delegating to an empty role. Unreachable with the
      > shipped `risk: safe` policy; a follow-up may persist the pending target.
      >
      > Follow-up found (pre-existing, out of scope): `Execute`'s panic recovery yields a FAILED
      > event on the wire but does not persist FAILED to the store (the record stays WORKING).

## Phase 6b: Reliability Fixes From Review (PR 6b)

> Review-driven: added after the Phase 6 reliability review returned four non-blocking
> WARNING findings. Not part of the original plan; no task above is renumbered or altered.

- [x] 6b.1 An unrecognized or empty peer state was reported with the escalation wording
      (`peer escalated (peer task "" is )`). `executeDelegation` now gives INPUT_REQUIRED its
      own arm and reports any other non-terminal state as a `port.Delegator` protocol violation.
- [x] 6b.2 A COMPLETED peer with empty output left the delegating agent's own text standing in
      as the result. `actionOutcome.Delegated` now gates both copies instead of a non-empty check.
- [x] 6b.3 `assertFailedNaming` matched needles against the whole log buffer (and one needle
      hardcoded slog quote escaping). Error text is now asserted on `executeDelegation`'s
      returned error directly; the helper keeps only the end-to-end guarantees it can observe.
- [x] 6b.4 The `defaultDelegateTimeout` fallback and the parent-cancellation guard were
      unproved. `fake.Delegator` now records the ctx deadline it observes, and tests assert both.
- [x] 6b.5 Swapped an unsynchronized `del.Calls[0]` read for the existing `LastCall()` accessor.

> Still open follow-ups (unchanged by this slice): `Execute`'s panic recovery yields a FAILED
> event but leaves the persisted record WORKING; the `strings.Contains`-on-`rec.Output`
> replace-vs-append semantics; the 5-second wall-clock threshold in the non-terminal immediacy test.

## Phase 7: Turn It On (PR 7a, 7b, 7c)

> **Delivered as three chained slices, not one PR.** Implemented whole first, then split along
> commit boundaries with the combined tree byte-identical to the single-PR version:
>
> | Slice | Contents | Authored lines |
> |-------|----------|----------------|
> | 7a | `agents/ceo.md`, `agents/engineer.md`, this task record | 191 (documentation only) |
> | 7b | `transport/mcp/tools.go` and its two test files | 113 |
> | 7c | `cmd/company/wire.go`, `core/supervisor/integration_test.go` | 331 |
>
> Two independent reasons forced the split. First, the single slice measured 517 authored lines
> against the 400-line budget. Second, the four-lens adversarial review of the single slice could
> not complete: the `review-resilience` host agent was refused twice by the model provider's
> content filter, so that lineage stopped at `unachievable_lens_slot` with authority never
> approved. The `review-risk` lens captured successfully from the same input, so the candidate
> content alone is not the cause. Isolating the agent-persona markdown into a documentation-only
> slice removes the most plausible (though unproven) trigger from the code slices and brings every
> slice inside budget, so no `size:exception` is required. Each slice is independently green
> (`gofmt`, `go vet`, `go build`, `go test ./... -race -count=1`).

- [x] 7.1 Edit `company.yaml` — add `risk_policy.delegate_task: {risk: safe, allowed_roles:
      [ceo]}` (design D13: shipped as `safe` → `Permit`, so internal hand-offs don't require human
      approval by default; anything the peer subsequently does that leaves the company is
      independently classified against the peer's own role).
- [x] 7.2 RED: In `transport/mcp/tools_internal_test.go`, add a schema table case:
      `delegate_task → Required == [body, target]` and confirm `telegram_send → Required ==
      [body]` still holds. Confirm the `delegate_task` case fails (no branch exists yet).
- [x] 7.3 GREEN: Edit `transport/mcp/tools.go` — add `targetSchema()` helper and branch
      `buildInputSchema` on `kind == port.KindDelegateTask` to add the `target` argument to both
      `Properties` and `Required` (design D11, comparing against the exported
      `port.KindDelegateTask` constant so a rename is a compile error, not silent drift). Update
      `buildToolDescription` with a dedicated `delegate_task` branch so the tool's description
      mentions the required `target` argument identifies the delegation's target role. Confirm 7.2
      passes.
- [x] 7.4 RED: Add `TestRegisterTools_DelegateTaskRecordsTargetAndBody` — calling the
      `delegate_task` tool with `{target: "engineer", body: "..."}` records
      `ActionIntent{Kind: "delegate_task", Payload: {target: "engineer", body: "..."}}` in the
      sink and returns success synchronously, with no peer contacted as a direct result of the
      call.
- [x] 7.5 GREEN: 7.4 passes with no production-code change (verification, not new behavior),
      exactly as anticipated. Since supplying `target` was already accepted before 7.3 (the schema
      leaves `additionalProperties` permitted), no honest RED was obtainable by running the test
      as written — falsified instead: temporarily deleted `port.TargetArg` from the recorded
      `args` map inside the tool handler, confirmed only this test failed (`intent
      Payload["target"] = <nil>, want "engineer"`) while the sibling recording test stayed green,
      then reverted. Proves the test pins the whole-args-map recording path 7.3's `extractTarget`
      depends on.
- [x] 7.6 Create `agents/engineer.md` — a minimal peer persona (design OD4, resolved: ship it).
      States the engineer role's purpose, that it receives delegated tasks over A2A with no
      conversational continuation, and that its own risky actions (e.g. `telegram_send`) are still
      classified independently against its own role.
- [x] 7.7 Edit `agents/ceo.md` — add a "Delegating work to a peer" section: when and how to call
      `delegate_task` with `target` and `body`, and an explicit statement that the outcome is
      unavailable this turn (the CEO's own CLI process exits before the peer answers; only the
      task record carries the result).
- [x] 7.8 Edit `cmd/company/wire.go` — construct one shared `transport/a2a.Client` (using the
      `PeerDirectory` from Phase 3) and inject it into every supervisor's `Config.Delegator`;
      add `wireOptions.delegateTimeout` so `Config.DelegateTimeout` is configurable at the
      composition root (zero still defaults to 10 minutes inside `supervisor.New`, per D10). The
      `supervisor.New` error-propagation half of this task was already satisfied by Phase 2's
      `materializeAgents` error handling — no further change needed there.
- [x] 7.9 In `core/supervisor/integration_test.go`, deleted `TestIntegration_CEODelegatesToWorkerOverRealWire`
      and its stale comment. Added a real end-to-end replacement,
      `TestIntegration_CEODelegatesToEngineerOverRealWire`: two real `transa2a.Server` instances,
      the CEO's `fake.Provider` scripted to emit a `delegate_task` intent targeting `engineer`, the
      engineer's to return output, wired through a real `transport/a2a.Client` and
      `PeerDirectory`. Asserts `engineer.RunTaskCallCount() > 0` and the CEO task's output contains
      the engineer's text. Falsification (no honest RED existed — Phases 4-6 already implement the
      whole path): temporarily short-circuited `executeAction`'s `delegate_task` branch to
      unreachable (`&& false`), reran the test, confirmed it failed for the right reason (engineer
      never ran; CEO task FAILED instead of COMPLETED with empty output), then reverted.
- [x] 7.10 Confirmed 7.9's replacement test passes end-to-end through the real wire (real
      `transa2a.Server` instances, real `Client`, real `PeerDirectory`, real supervisor routing —
      only the CLI subprocess is faked, per repo convention).
- [x] 7.11 Added `TestIntegration_DelegatingCLIInvokedExactlyOnce` proving "the delegating agent's
      CLI is invoked exactly once for that task" (spec: agent-delegation "The Delegating Agent
      Process Never Observes the Peer's Result") — asserts the CEO's
      `fake.Provider.RunTaskCallCount() == 1` across the whole delegate-then-complete chain.
- [x] 7.12 Added `TestIntegration_DelegationTimeoutLeavesPeerRunning`: a `blockingUntilReleased`
      provider parks the engineer mid-`RunTask` while the CEO delegates with a 100ms
      `DelegateTimeout`; after the CEO task FAILS on timeout, `Client.PeerTaskState` against the
      engineer's real task ID reports it still `WORKING` — never canceled, rejected, or altered as
      a side effect of the delegator's timeout.
- [ ] 7.13 **Not run.** `company.yaml` at the repo root is gitignored (`.gitignore` line 27,
      `company*.yaml`, under "Local configuration") — there is no git-tracked company config in
      this repository for any manual acceptance run to exercise, and the spec's "shipped
      company.yaml" scenarios (company-as-code) cannot be satisfied by a git-committed file here;
      task 7.1's edit was applied to the local, uncommitted `company.yaml` already present on this
      machine, for exactly this manual-run purpose, but it is not part of this PR's diff. Beyond
      that: `ACME_AUTH_TOKEN`, `TELEGRAM_BOT_TOKEN`, and `TELEGRAM_OWNER_ID` are unset in this
      environment, and no live Anthropic auth session for the `claude` CLI (present at
      `~/.local/bin/claude`) was available to complete a real two-agent run. A human must: set
      those three env vars, ensure `claude` is authenticated, run `go run ./cmd/company
      materialize company.yaml`, send the CEO a task that prompts a `delegate_task` call, and
      confirm the engineer's supervisor receives the A2A `SendMessage` with the CEO task's output
      containing the engineer's terminal text.
- [x] 7.14 Ran `gofmt -l .` (empty), `go build ./cmd/company`, `go vet ./...`, and `go test ./...
      -race -count=1` — all green, and re-verified independently on each of the three slices 7a,
      7b and 7c. Budget: the work measured 517 authored changed lines as a single slice, over the
      400-line budget, so it was split into 7a (191), 7b (113) and 7c (331) as recorded at the top
      of this phase. Every slice now lands inside the 400-line budget and no `size:exception` is
      carried. The combined tree of the three slices is byte-identical to the unsplit version.

## Phase 8: Final Cross-Slice Regression Sweep

- [ ] 8.1 Re-run `go test ./... -race -count=1` once all seven PR slices have merged, to catch any
      cross-slice interaction the per-slice test runs in Phases 1–7 could not see in isolation
      (for example, Phase 4's `PeerTaskState` reading Phase 5's output-on-the-wire change against
      a real peer).
- [ ] 8.2 Confirm no code, comment, test, or doc file introduced by this change references
      `AwaitingPeerRole`, `AwaitingPeerTaskID`, `AwaitingPeerDeadline`, `TaskResumer`,
      `SetResumer`, a watcher goroutine, `companyUIAdapter`, or a UI awaiting-peer badge — a
      accidental leak of deferred-scope (slices 7–9 in the design's numbering, not this
      document's Phase 7) work into this change would silently duplicate work the follow-up
      change `agent-delegation-chained-approval` is responsible for.
- [ ] 8.3 Confirm `core/supervisor/store.go`'s `TaskRecord` gained no new fields in this change —
      the awaiting-peer fields belong entirely to the deferred follow-up.
