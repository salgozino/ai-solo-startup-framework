# Proposal: Chained Approval for Delegated Peer Escalations

Make a peer's own escalation resolvable. `agent-delegation-over-a2a` ships delegation with a
blocking wait that only understands a terminal peer: when the peer's task comes back
non-terminal — because the peer escalated to `INPUT_REQUIRED` and awaits its own human verdict —
the delegating task fails with an explicit "the peer escalated; chained approval is not yet
wired" error. That is honest, and it is a dead end. This change turns that dead end into a single
human approval on the peer's task that automatically resumes the delegating task, makes the
delegating task's awaiting-peer wait distinguishable from an ordinary self-escalation, recovers
that wait across a restart, and — first — makes the peer's approval control reachable in the
monitoring UI at all.

**Affected agent roles**: `ceo` (delegates, then parks awaiting a peer), `engineer` (receives
delegation and is the role whose escalation must become approvable). Any role granted
`delegate_task` in `risk_policy` can park awaiting a peer; any role granted a risky action kind
can be the escalating peer.

## Intent

`agent-delegation-over-a2a` deliberately stopped short. It delivers design slices 1–6 and 10 —
the delegation leg, the A2A client, the peer directory, the supervisor routing, and the shipped
`company.yaml` entry. It defers design slices 7, 8 and 9 (~840 lines of already-written design)
to this change.

The split is not arbitrary, and it is not "the hard part left for later" in the pejorative sense:

| Fact | Consequence |
|---|---|
| The shipped `company.yaml` grants `telegram_send` with `allowed_roles: [ceo]` only. | An `engineer`-role peer attempting a risky action is `HardDeny`ed by the existing capability check, never `Escalate`d. |
| `delegate_task` ships with `allowed_roles: [ceo]` too (design D13). | The peer has no action kind it is permitted to attempt, so it has nothing to escalate. |
| Therefore, with the shipped configuration, a peer's task can never reach `INPUT_REQUIRED`. | The chained-approval path is **unreachable** in the shipped demo. Shipping it in the parent change would have added ~840 unexercised lines to prove nothing an operator could observe. |

So this is framework correctness, not demo functionality: it is required the moment a company
grants a non-CEO role any risky action kind, and it is required for the parent change's interim
error to stop being the terminal answer. It is not required for the parent change's shipped
`company.yaml` to work end to end.

**Ordering constraint, non-negotiable**: design **slice 9 (UI aggregation) MUST land before
slice 7 (chained approval)**. `cmd/company/main.go:67` binds the monitoring UI to
`runtimes[0].uiAdap` — the CEO's supervisor only — and `main.go:72` registers `SetOnStateChange`
on that runtime only. A peer's `INPUT_REQUIRED` task lives on a different supervisor and is
therefore invisible and unapprovable. A chained approval merged without UI aggregation would
park the delegating task on a human verdict the human has no control to give: every chained
delegation could only ever time out. Slice 9 is the prerequisite, not the polish.

## Scope

### In Scope

- **Chained approval (design slice 7)**: the `TaskRecord` awaiting-peer field set
  (`AwaitingPeerRole`, `AwaitingPeerTaskID`, `AwaitingPeerDeadline`), `port.TaskResumer` +
  `Supervisor.SetResumer`, the detached `GetTask`-polling watcher (design D1/D2), the
  `executeResume` awaiting-peer precedence branch (design D4), `AwaitPeerTimeout`,
  `fake.Resumer`, and a two-server integration test. This replaces the parent change's interim
  "peer escalated; chained approval not yet wired" failure path.
- **Restart recovery of an awaiting-peer wait (design slice 8)**: `filterAwaitingPeerTasks`,
  watcher restart on the **persisted** original deadline, transient-vs-fatal peer-directory
  error handling, and the honest-failure path when the peer can no longer be reached.
- **Monitoring UI aggregation (design slice 9, lands first)**: a `companyUIAdapter` in `wire.go`
  implementing `ui.Supervisor` across every runtime, `ui.TaskRecord` gaining `Agent`,
  `AwaitingPeerRole`, `AwaitingPeerTaskID`, `ui.ErrAwaitingPeer` (→ HTTP 409) on a verdict posted
  against a delegating task, the `app.js` non-actionable "awaiting peer" badge, and
  `SetOnStateChange` registered on every runtime so a peer transition also pushes SSE.

### Out of Scope

- Everything `agent-delegation-over-a2a` already covers: the delegation action kind, the A2A
  client, the peer directory, the synchronous routing half, the dead-code removal, and the
  shipped `company.yaml` / `agents/*.md` entries. This change modifies that surface; it does not
  re-specify it.
- Granting a non-CEO role a risky action kind in the shipped `company.yaml`. The chained path
  stays unreachable in the shipped demo by configuration; this change makes it correct, not
  exercised by default.
- Peer-to-peer delegation beyond one hop, and any hop counter / delegation-depth field. The
  existing `allowed_roles` capability check remains the entire loop guard (unchanged from the
  parent change).
- Streaming delegation (`SendMessageStream`). Still blocking-only.
- Persisting the peer's `a2asrv` task store. A peer task that completed *before* a restart is
  gone from the peer's fresh in-memory store; this change fails honestly in that window rather
  than recovering the result (parent design, Open Risks).
- Capping the number of concurrent watcher goroutines. Carried forward as an open question.

## Capabilities

> Contract with `sdd-spec`. Both target specs are PENDING and UNARCHIVED: `agent-delegation` is
> created by `agent-delegation-over-a2a`; `agent-supervisor` is created by
> `agent-startup-framework-foundation` and already modified by `agent-delegation-over-a2a`.

### Modified Capabilities

- `agent-delegation`: adds chained approval on a peer escalation, the awaiting-peer /
  self-escalation distinction in the persisted record and derived SSE/UI surface, and restart
  recovery of an awaiting-peer wait. Supersedes the parent change's interim
  "peer escalated; chained approval not yet wired" failure requirement.
- `agent-supervisor`: "Crash and Restart Recovery" gains the awaiting-peer recovery obligation
  (re-establish the wait, or fail honestly — never silently resubmit, never leave an
  unresolvable `INPUT_REQUIRED`).

### New Capabilities

None. This change modifies capabilities the parent change and the foundation change create.

## Approach

The parent change ends at the synchronous half. This change adds the asynchronous half behind
the same `port.Delegator`, and makes the resulting state visible.

```
CEO task: delegate_task Permitted
  └─ Delegator.Delegate(ctx, role, body)
       ├─ peer terminal        → copy output, COMPLETE / FAIL   [parent change]
       └─ peer INPUT_REQUIRED  → park delegating task + spawn detached watcher   NEW
            │   record: AwaitingPeerRole / AwaitingPeerTaskID / AwaitingPeerDeadline
            │
            │   human sees the PEER's approval control (UI aggregation, slice 9)  NEW
            │   human sees the DELEGATOR as a non-actionable "awaiting peer" badge
            │
            └─ watcher polls Delegator.PeerTaskState every PeerPollInterval
                 └─ peer terminal | deadline expired | watcher panic
                      └─ Resumer.Resume(ctx, delegatingTaskID)
                           └─ executeResume re-queries PeerTaskState once and decides
                                → COMPLETED with the peer's output, or FAILED naming the peer
```

All architecture decisions are already made and live in
`openspec/changes/agent-delegation-over-a2a/design.md` — they are not re-opened here:

| Decision | Choice | Why it stays |
|---|---|---|
| D1 | `GetTask` polling, not `SubscribeToTask` | Verified against `a2a-go/v2 v2.5.0`: `execManager.Resubscribe` fails with `"no active execution"` once the peer's executor iterator has returned, which is exactly when a peer parks. |
| D2 | The watcher holds zero authority; `executeResume` decides | One resolution path serves both the live case and the restart case; a dead watcher cannot corrupt state. |
| D3 | Resume through the existing A2A resume path via a narrow `port.TaskResumer` | Every transition keeps flowing through `Execute`; no second execution path into a task. |
| D4 | Awaiting-peer vs self-escalation distinguished by which optional field set is populated | Test-asserted invariant; `omitempty` fields, so pre-change records deserialize unchanged. `executeResume` MUST check awaiting-peer **first**. |
| D5 | The monitoring UI aggregates all supervisors | Without it the required approval control is unreachable. **Slice 9 lands before slice 7.** |
| D6 | `port.Delegator` has two methods (`Delegate`, `PeerTaskState`) | Already shipped by the parent change; re-establishing a wait after restart with `Delegate` alone would start a second peer task. |

## Affected Areas

| Area | Impact | Description |
|---|---|---|
| `core/supervisor/store.go` | Modified | `TaskRecord` gains `AwaitingPeerRole`, `AwaitingPeerTaskID`, `AwaitingPeerDeadline` (all `omitempty`). |
| `core/port/delegator.go` | Modified | Add `TaskResumer`. |
| `core/supervisor/supervisor.go` | Modified | `SetResumer`; park-on-non-terminal replacing the interim error; watcher spawn; `executeResume` awaiting-peer precedence; `AwaitPeerTimeout`, `PeerPollInterval`, clock. |
| `core/supervisor/recovery` path | Modified | `filterAwaitingPeerTasks`; watcher restart on the persisted deadline; honest failure when the peer is unreachable. |
| `core/port/fake/` | Modified | `fake.Resumer`; `fake.Delegator` gains parked-peer behavior. |
| `ui/` (`ui.Supervisor`, `ui.TaskRecord`, `app.js`) | Modified | `Agent`, `AwaitingPeerRole`, `AwaitingPeerTaskID`; `ErrAwaitingPeer` → HTTP 409; non-actionable badge. |
| `cmd/company/wire.go` | Modified | `companyUIAdapter` over all runtimes; `SetResumer` after `transa2a.New`. |
| `cmd/company/main.go` | Modified | Bind the UI to the aggregating adapter; `SetOnStateChange` on every runtime. |
| `company.yaml`, `agents/*.md` | Unchanged | No configuration change; the path stays unreachable in the shipped demo by design. |

## Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Slice 7 merged before slice 9 — the approval control is unreachable and every chained delegation times out | High if the ordering is not enforced | Stated as a hard ordering constraint in this proposal, in the parent `design.md` PR Slicing note, and to be encoded as a task dependency by `sdd-tasks`. |
| `executeResume` falls into the existing "re-execute from scratch" branch for an awaiting-peer record (it has no `PendingIntentKind`) and re-runs the provider | High | The awaiting-peer branch MUST precede the `PendingIntentKind == ""` branch; violating it breaks "the delegating agent's CLI is invoked exactly once", which is a parent-change requirement with its own test. |
| Watcher `Resume` races a human verdict → `ErrExecutionInProgress` on one of them | Low | `PostVerdict` rejects awaiting-peer tasks up front (`ErrAwaitingPeer`), and the awaiting-peer resume branch is idempotent (it re-queries). Narrow but real. |
| A peer task that completed before a restart is gone from the peer's fresh in-memory `a2asrv` store | Medium | Spec-compliant honest failure naming the peer; persisting the peer task store is a further follow-up. |
| Unbounded concurrent watcher goroutines | Low | Naturally small with `allowed_roles: [ceo]`; the bound is incidental, not enforced. Open question. |
| `PeerPollInterval` default of 5s sets the human-approval-to-completion lag | Low | Configurable; confirm or justify the number during design/apply. |
| Three slices (~840 lines) exceed the 400-line review budget | High (certain) | Delivery is `auto-chain`; ship as three PRs in the order 9 → 7 → 8. |

## Rollback Plan

1. **Config-level rollback (no code change)**: the chained path is only reachable when a non-CEO
   role is granted a risky action kind. The shipped `company.yaml` grants none, so reverting to
   the shipped configuration disables the path entirely.
2. **Code-level rollback**: revert the chained PRs in reverse order — slice 8, then slice 7, then
   slice 9. Reverting slice 7 restores the parent change's interim
   "peer escalated; chained approval not yet wired" failure, which is a valid shipped state.
   Slice 9 (UI aggregation) may stay merged independently: it is an additive UI improvement with
   no dependency on the awaiting-peer fields beyond rendering them when present.
3. **Persisted data**: the three `TaskRecord` fields are `omitempty`. Records written while this
   change was live deserialize after a revert with the fields ignored; such a task falls back to
   the pre-change reading ("self-escalation or ordinary task"). A task parked awaiting a peer at
   the moment of rollback must be resolved manually — call this out in the rollback runbook.

## Dependencies

- **`agent-delegation-over-a2a` MUST be merged first.** This change modifies its `agent-delegation`
  spec, supersedes its interim non-terminal-peer failure requirement, and builds on
  `port.Delegator`, the A2A client, the peer directory, and the supervisor routing it delivers.
- `openspec/changes/agent-delegation-over-a2a/design.md` — carries design slices 7, 8 and 9
  (~840 lines) and decisions D1–D5, which are the design input for this change and are not
  re-derived here.
- `github.com/a2aproject/a2a-go/v2 v2.5.0` — `a2aclient.GetTask` is the polling mechanism (D1).

## Success Criteria

- [ ] With a non-CEO role granted a risky action kind, a delegated peer that escalates parks the
      delegating task in `INPUT_REQUIRED` with `AwaitingPeerRole`, `AwaitingPeerTaskID` and
      `AwaitingPeerDeadline` populated — and the parent change's interim
      "chained approval not yet wired" failure no longer occurs.
- [ ] The monitoring UI lists tasks from **every** runtime, each stamped with its agent name, and
      a peer transition pushes SSE — verified before any chained-approval code is merged.
- [ ] Exactly one actionable approve/reject control is rendered for a chained escalation, on the
      peer's task; the delegating task renders a non-actionable "awaiting peer" badge, and a
      verdict posted against it returns HTTP 409.
- [ ] After the human approves the peer and the peer reaches a terminal state, the delegating
      task auto-resumes with no second human action, completing with the peer's output (or
      failing per the parent change's result-copy requirement).
- [ ] A restart while a delegating task is parked awaiting a peer re-establishes the wait against
      the **persisted original** deadline, or drives the task to an explicit failure naming the
      unreachable peer — never a nil-pointer panic, never a permanently unresolvable
      `INPUT_REQUIRED`.
- [ ] The delegating agent's provider (CLI subprocess) is still invoked exactly once per task
      across park, watch, and resume — the parent change's requirement still holds.
- [ ] A table-driven test asserts the record invariant: for any `INPUT_REQUIRED` record, exactly
      one of `PendingIntentKind != ""` or `AwaitingPeerTaskID != ""` holds.
- [ ] `go build ./cmd/company`, `go vet ./...`, `go test ./... -race` green; every PR slice
      ≤ 400 authored changed lines or carries an accepted `size:exception`.
