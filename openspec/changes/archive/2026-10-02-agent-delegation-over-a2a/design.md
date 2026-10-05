# Design: Agent Delegation Over A2A

The CEO agent gains one new leg at the end of the existing action-intent pipeline: a Permitted
`delegate_task` intent is executed through a narrow `port.Delegator` instead of `Gateway.Send`.
Everything upstream — MCP tool, intent sink, `Classify` → HardDeny/Escalate/Permit — is unchanged.
Everything the delegation adds (peer addressing, bearer auth, tenant propagation, blocking wait)
lives behind that one port, in `transport/a2a`. Chained approval and awaiting-peer restart
recovery are designed below but **deferred** to `agent-delegation-chained-approval`; they live
behind the same port, which is why the port shape accommodates them today.

## Technical Approach

```
CEO agent (claude CLI)                                        [unchanged]
  └─ MCP tools/call delegate_task {target, body}              [schema gains target]
       └─ intent sink → ActionIntent{Kind, Payload}           [unchanged]
            └─ policy.Classify(kind, role, riskPolicy)        [unchanged]
                 ├─ HardDeny → REJECTED                       [unchanged — the loop guard]
                 ├─ Escalate → INPUT_REQUIRED → human → resume [unchanged]
                 └─ Permit  → executeAction
                      ├─ kind == telegram_send → Gateway.Send [unchanged]
                      └─ kind == delegate_task → Delegator.Delegate(ctx, role, body)   NEW
                           └─ transport/a2a client → peer /invoke (bearer, tenant)
                                ├─ peer terminal  → copy output, COMPLETE / FAIL
                                └─ peer INPUT_REQUIRED → park + detached watcher      NEW
```

Three facts drove every hard decision below, and all three were verified against
`a2a-go/v2 v2.5.0` source, not assumed:

| Verified fact | Source | Consequence |
|---|---|---|
| `SubscribeToTask` → `execManager.Resubscribe` fails with `"no active execution"` once the executor's iterator has returned | `internal/taskexec/local_manager.go:134-148`, `:265` | A parked peer **cannot** be watched by subscription. Polling `GetTask` is the only option (D1). |
| `AuthInterceptor.Before` returns early — attaching **no** header — unless `SessionIDFrom(ctx)` succeeds | `a2aclient/auth.go:72-75` | The client must call `AttachSessionID` on every outbound call or it silently sends unauthenticated (D8). |
| The supervisor yields `NewStatusUpdateEvent(execCtx, COMPLETED, nil)` — a **nil** message | `core/supervisor/supervisor.go:396` | The peer's output does not currently cross the wire at all. It must be attached to the terminal event (D9). |

---

## Architecture Decisions

### D1 — Awaiting a parked peer: `GetTask` polling, not `SubscribeToTask`

**Choice**: a detached watcher goroutine polls `Delegator.PeerTaskState` (which calls
`a2aclient.GetTask`) every `PeerPollInterval` (default 5s) until the peer's task is terminal or the
await deadline passes.

**Alternatives considered**:

| Alternative | Why rejected |
|---|---|
| `SubscribeToTask` stream | **Does not work.** `defaultRequestHandler.SubscribeToTask` delegates to `execManager.Resubscribe`, which looks the task up in `m.executions` — a map the manager `delete`s the moment the executor's iterator returns (`local_manager.go:265`). Our peer escalates by yielding `INPUT_REQUIRED` and **returning**, so by the time we would subscribe the execution is already unregistered and the call fails with `ErrTaskNotFound: no active execution`. |
| Server-side push (`PushConfig` / webhooks) | Requires the peer to hold an outbound callback URL for the delegator, inverting the directory and adding a second inbound surface per agent. Large change for a latency win we do not need. |
| Blocking the delegating `Execute` iterator until the peer resolves | Would prevent the delegating task ever persisting as `INPUT_REQUIRED`, and would keep the delegator's own a2asrv execution live — so the resume `SendMessage` would be rejected with `ErrExecutionInProgress`. Also defeats the entire point of parking. |

**Rationale**: polling is the only mechanism the installed library actually supports for a
non-active task, it costs one cheap JSON-RPC call per interval per parked delegation, and it
degrades gracefully across a restart (the same call works against a freshly recovered peer task).
The 5s worst-case latency between human approval and the delegating task completing is acceptable
and is configurable.

### D2 — The watcher holds zero authority; `executeResume` decides

**Choice**: the watcher goroutine does exactly one thing — when the peer reaches terminal **or** the
deadline expires **or** the watcher itself panics, it calls `Resumer.Resume(ctx, delegatingTaskID)`.
It never writes a `TaskRecord`, never decides COMPLETED vs FAILED, never touches the FSM.
`executeResume` then re-queries `PeerTaskState` once and decides everything.

**Alternatives considered**: the watcher writes the outcome into the record and the resume path
trusts it. Rejected: it creates two writers for one record across two goroutines, and it needs a
*second*, different code path for the restart case (where no watcher ever observed the transition).

**Rationale**: a dumb watcher means **one** resolution path serves both the live case and the
restart case. It also means a dead watcher cannot corrupt state — worst case the task stays
`INPUT_REQUIRED` and restart recovery picks it up, which is exactly the spec's required failure
mode ("never a wait that can no longer ever resolve", because the deadline is persisted).

### D3 — Resuming through the existing A2A resume path, via a narrow `port.TaskResumer`

The `Execute` iterator that parked the task is gone; its `yield` is dead. The watcher must
re-enter through `handler.SendMessage` with the original `TaskID` — the same mechanism
`supervisorUIAdapter.PostVerdict` (`wire.go:122-156`) and `RecoverOpenTasks`
(`supervisor.go:132-145`) already use and that `a2asrv` recognises as a resume
(`ExecutorContext.StoredTask != nil`).

The supervisor cannot hold `a2asrv.RequestHandler` directly: `transa2a.New(sup, ...)` takes the
supervisor, so the handler does not exist at `supervisor.New` time. Resolved with a post-construction
setter, mirroring the existing `SetOnStateChange`:

```go
// core/port/delegator.go

// TaskResumer re-enters a parked task on the supervisor's own A2A handler.
// It exists so the supervisor can resume a task from a goroutine other than the
// one that parked it, without core/ importing a2asrv or knowing about transports.
type TaskResumer interface {
    // Resume delivers input to taskID as an A2A resume. Returns an error if the
    // task is unknown or another execution for it is already in progress.
    Resume(ctx context.Context, taskID, input string) error
}
```

`Supervisor.SetResumer(port.TaskResumer)` is called by `wire.go` right after `transa2a.New` returns.
A nil resumer is not a construction error (the supervisor is usable without delegation), but
attempting to park an awaiting-peer wait with a nil resumer fails the task explicitly rather than
parking a wait nothing can ever resolve.

**Rejected**: a supervisor-internal channel/callback that bypasses `SendMessage`. It would be a
second execution path into a task — precisely the class of bug that made `executeDelegation` dead
code. Every state transition keeps flowing through `Execute`.

### D4 — Distinguishing "awaiting peer" from "awaiting your approval"

Both are `INPUT_REQUIRED`. The record distinguishes them by **which optional field set is populated**,
with a hard, test-asserted invariant.

```go
// core/supervisor/store.go — TaskRecord gains:

// AwaitingPeerRole is the target role of an in-flight delegation this task is
// parked on. Non-empty means this task's INPUT_REQUIRED is a chained delegation
// wait, NOT a self-escalation awaiting a human verdict.
AwaitingPeerRole string `json:"awaiting_peer_role,omitempty"`
// AwaitingPeerTaskID is the peer's A2A task ID being awaited.
AwaitingPeerTaskID string `json:"awaiting_peer_task_id,omitempty"`
// AwaitingPeerDeadline is the RFC3339 instant after which the wait fails.
// Persisted so a restart honors the ORIGINAL deadline, not a fresh one.
AwaitingPeerDeadline string `json:"awaiting_peer_deadline,omitempty"`
```

**Invariant** (asserted by a table-driven test over both shapes):
for any record in `INPUT_REQUIRED`, exactly one of `PendingIntentKind != ""` (self-escalation) or
`AwaitingPeerTaskID != ""` (chained wait) holds.

`executeResume` therefore checks **awaiting-peer first**, before the existing `PendingIntentKind == ""`
branch — otherwise an awaiting-peer record (which has no pending intent) would fall into the
"re-execute from scratch" branch at `supervisor.go:267-277` and re-run the provider, violating
"the delegating agent's CLI is invoked exactly once".

All three fields are `omitempty`, matching the `PendingIntentBody` precedent — records written
before this change deserialize unchanged.

**Rejected**: a single `WaitKind string` enum field. It carries less information (we need the peer's
task ID and the deadline anyway) and would require a migration for old records.

### D5 — The monitoring UI must aggregate all supervisors

**This is scope the proposal did not capture, and the spec forces it.**

`main.go:67` binds the UI to `runtimes[0].uiAdap` — the CEO's supervisor only. The peer's
`INPUT_REQUIRED` task lives on a *different* supervisor and is therefore **invisible today**. The
spec requires the human to approve "exactly once, on the peer's task". With a CEO-only UI, that
control is unreachable and every chained delegation could only ever time out.

**Choice**: a `companyUIAdapter` in `wire.go` implementing `ui.Supervisor` over all runtimes.

| Method | Behavior |
|---|---|
| `ListTasks` | concatenation across every runtime, each record stamped with its agent name |
| `PostVerdict(taskID, approve)` | routes to the runtime that owns `taskID`; returns `ui.ErrAwaitingPeer` (→ HTTP 409) if that record has `AwaitingPeerTaskID != ""` |
| `SendTask` | CEO only (`runtimes[0]`) — unchanged human-talks-to-the-CEO semantics |
| `StatusStr` | CEO's, unchanged |

`main.go` calls `SetOnStateChange` on **every** runtime so a peer transition also pushes SSE.

`ui.TaskRecord` gains `Agent`, `AwaitingPeerRole`, `AwaitingPeerTaskID`. `ui/app.js` renders:

```
isInput && !t.awaiting_peer_task_id  → <Approve> <Reject>          (actionable, unchanged)
isInput &&  t.awaiting_peer_task_id  → "Awaiting engineer · <id>"  (non-actionable badge)
```

**Rejected**: leaving the UI CEO-only and rendering the delegator's wait as non-actionable. That
satisfies the letter of "never two controls" while making the one required control unreachable —
the requirement would pass and the feature would not work.

### D6 — `port.Delegator` has two methods, not one

The proposal (D3) promised a single-method port. The spec's chained-approval and restart-recovery
requirements — added after the proposal — make that impossible: re-establishing a wait after a
restart with only `Delegate` would start a **second** peer task. Deliberate, spec-forced deviation.

```go
// core/port/delegator.go
package port

// KindDelegateTask is the action-kind string for delegation. Single source of truth
// shared by the MCP tool schema (which must require a target argument for it) and the
// supervisor's routing switch — a rename is a compile error, not a silent drift.
const KindDelegateTask = "delegate_task"

// TargetArg is the ActionIntent payload key carrying the delegation's target ROLE.
const TargetArg = "target"

// DelegationResult is the observed state of a peer task.
type DelegationResult struct {
    // PeerTaskID is the peer's A2A task ID. Always set on a nil-error return,
    // including when the peer parked in INPUT_REQUIRED.
    PeerTaskID string
    // State is the peer task's A2A TaskState string as of this call
    // (e.g. "TASK_STATE_COMPLETED", "TASK_STATE_INPUT_REQUIRED").
    State string
    // Output is the peer's terminal text. Empty unless State is COMPLETED.
    Output string
}

// Delegator sends work to a role-addressed peer agent over A2A.
// Implementations live in transport/a2a; core/ never imports an A2A client.
type Delegator interface {
    // Delegate sends body to the peer fulfilling role and blocks until that peer's
    // task stops advancing — either a terminal TaskState, or INPUT_REQUIRED (the peer
    // escalated and awaits its own human verdict). Any other non-terminal state is a
    // protocol violation and MUST be returned as an error, never as a result.
    Delegate(ctx context.Context, role, body string) (DelegationResult, error)

    // PeerTaskState reports the current state and terminal output of a peer task
    // previously returned by Delegate. Used both to await a parked peer and to
    // re-establish that wait after a process restart.
    PeerTaskState(ctx context.Context, role, peerTaskID string) (DelegationResult, error)
}
```

The supervisor distinguishes terminal from non-terminal with `a2a.TaskState(res.State).Terminal()`
— `core/supervisor` already imports `a2a`, so no hand-rolled state list and no new dependency.

`executeAction`'s signature changes from `error` to `(actionOutcome, error)` rather than smuggling
the parked case through a sentinel error, because both call sites (`executeWithPolicy` Permit and
`executeResume` approved-escalation) must handle it:

```go
// actionOutcome reports what executeAction did beyond succeeding or failing.
type actionOutcome struct {
    // AwaitingPeerRole / AwaitingPeerTaskID are set when a delegation parked
    // awaiting a peer's own escalation. The caller must park the delegating task.
    AwaitingPeerRole   string
    AwaitingPeerTaskID string
    // Output is the peer's terminal text for a completed delegation; empty otherwise.
    Output string
}
```

**Port deletions** (`provider-adapter` spec): `port.Provider` drops `SendMessage`,
`SendMessageStream`, `ResolveAgent`; the `port.StreamEvent` type is deleted; both adapters drop
their six `errNotImplemented` stubs and the false "provided by transport/a2a" comment;
`fake.Provider` drops those three methods plus `SendMessageCall`, `MsgCalls`, `ReturnStream`,
`ReturnAddress`, and `SendMessageCallCount`. `SendTask` stays — the spec enumerates it in the
narrowed method set, even though it too has no production caller.

### D7 — Peer directory: fixed declared set, incrementally bound URLs

```go
// transport/a2a/directory.go

// ErrUnknownRole means the role is not declared in company.yaml. Permanent.
var ErrUnknownRole = errors.New("unknown role")
// ErrPeerNotRegistered means the role is declared but its server has not bound yet. Transient.
var ErrPeerNotRegistered = errors.New("peer not yet registered")

// PeerDirectory maps agent role → live A2A base URL. The declared role set is fixed at
// construction from company.yaml; base URLs are published one at a time as each agent's
// server binds. Splitting the two lets callers distinguish "no such role, ever" from
// "not up yet, retry" — a distinction the delegation watcher depends on.
type PeerDirectory struct {
    mu       sync.RWMutex
    declared map[string]struct{}
    bound    map[string]string
}

func NewPeerDirectory(roles []string) (*PeerDirectory, error) // duplicate role → error
func (d *PeerDirectory) Bind(role, baseURL string) error      // undeclared or rebind → error
func (d *PeerDirectory) BaseURL(role string) (string, error)  // ErrUnknownRole | ErrPeerNotRegistered
```

**Mutability and concurrency**: write-once-per-role after construction, guarded by `sync.RWMutex`.
Task goroutines call `BaseURL` (read lock) while the `materializeAgents` loop still calls `Bind`
(write lock). `declared` is immutable after construction and could be read lock-free, but stays
under the same mutex — simpler, and clean under `-race` with no reasoning required.

**Why two errors matter operationally**: recovery runs inside `transa2a.New`, *before* the wire loop
has bound later agents. A watcher restarted during that window would see `ErrPeerNotRegistered` and
must **keep polling**; only `ErrUnknownRole` — decidable at t=0 from `company.yaml` — is fatal. The
spec's two-named-error requirement is load-bearing, not cosmetic.

**Duplicate role**: `NewPeerDirectory` is called at the top of `materializeAgents`, *before* the
agent loop, so a duplicate fails materialize before `transa2a.New` (which calls `sup.MarkReady()`)
runs for any agent — satisfying "no supervisor in that company is marked ready".

**Rejected**: two-phase startup (bind all listeners, then construct supervisors). It would require
splitting `transa2a.New`, which currently listens, recovers, and marks ready in one call. The
declared-vs-bound split gets the same safety with no restructuring.

### D8 — A2A client: session-scoped credentials are mandatory

```go
// transport/a2a/client.go  (sketch — error handling elided)

type Client struct {
    dir      *PeerDirectory
    tenant   string
    creds    *a2aclient.InMemoryCredentialsStore
    session  a2aclient.SessionID
    poll     time.Duration
    resolver *agentcard.Resolver
}

func (c *Client) Delegate(ctx context.Context, role, body string) (port.DelegationResult, error) {
    base, err := c.dir.BaseURL(role)                       // ErrUnknownRole | ErrPeerNotRegistered
    card, err := c.resolver.Resolve(ctx, base)             // spec: card BEFORE any send
    peer, err := a2aclient.NewFromCard(ctx, card,
        a2aclient.WithInterceptors(&a2aclient.AuthInterceptor{Service: c.creds}))

    // MANDATORY: AuthInterceptor.Before returns with NO header attached unless a
    // SessionID is on the context (a2aclient/auth.go:72-75). Omitting this makes
    // every outbound request unauthenticated and every peer reject it with 401.
    ctx = a2aclient.AttachSessionID(ctx, c.session)

    msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(body))
    res, err := peer.SendMessage(ctx, &a2a.SendMessageRequest{Tenant: c.tenant, Message: msg})
    task, ok := res.(*a2a.Task)                            // non-Task result → protocol error
    return resultFrom(task), nil
}
```

The credentials store is seeded once at construction with
`session → {a2a.SecuritySchemeName("bearer"): AuthCredential(authToken)}`. The scheme name **must**
be the literal `"bearer"` — it is the map key the server publishes in its card
(`server.go:222-227`), and `AuthInterceptor` matches on that key.

`SendMessage` blocks until the peer's executor iterator finishes
(`a2asrv/handler.go:301-333` drains the subscription), so a `Delegate` return carries either a
terminal task or the parked `INPUT_REQUIRED` task. No extra client-side waiting is needed for the
first leg.

The stale comment at `core/supervisor/integration_test.go:88-92` claiming the client cannot attach a
Bearer header is wrong for v2.5.0 and dies with the test that carries it.

### D9 — The peer's output must be put on the wire

`executeWithPolicy` currently completes with `NewStatusUpdateEvent(execCtx, COMPLETED, nil)` — a
**nil** message. `a2aevent.applyStatusUpdate` sets `task.Status = event.Status` verbatim, so the
stored A2A task's `Status.Message` is nil and **no output crosses the wire**. Copying the peer's
output into the delegating task is impossible until this is fixed.

**Choice**: the terminal `COMPLETED` event carries `rec.Output` as a text part. `Delegate` and
`PeerTaskState` read `task.Status.Message` back out.

**Rejected**: `TaskArtifactUpdateEvent`. Artifacts are the semantically correct home for structured
results, but they add an event type to the supervisor's emit path and a second read path on the
client for zero benefit on plain text. Revisit when outputs stop being strings.

`FAILED` keeps the existing deliberately-generic `errorMessage(err)` ("task execution failed") so
internal paths are not leaked; the *delegating* task's failure error names the peer role and states
that the peer failed, fabricating no output — exactly what the spec asks.

**Blast radius (accepted)**: every completed task, delegated or not, now carries its output text in
its terminal A2A event. Strictly more honest; no consumer regresses.

### D10 — Two timeouts, because they bound two different things

| Field (`supervisor.Config`) | Default | Bounds | Why it is separate |
|---|---|---|---|
| `DelegateTimeout` | 10 min | the initial blocking `Delegate` (card resolution + `SendMessage`) | The peer is actively burning a CLI subprocess, and this call **holds the delegating supervisor's task goroutine**. This is the proposal's D4 timeout, unchanged. |
| `AwaitPeerTimeout` | 10 min | the awaiting-peer watch, measured from the instant the peer parked | The peer is waiting on a **human**, and the delegating goroutine is already free. Different clock, different cost, different natural duration. |

Both zero-value to 10 minutes and are settable at the composition root. Deliberately **not** one
shared budget: a single number either starves the human on the second leg or pins a goroutine too
long on the first. `AwaitPeerTimeout` is an addition the chained-approval and restart requirements
force; the spec's literal "the delegation port's blocking operation MUST enforce a timeout,
defaulting to 10 minutes" is satisfied by `DelegateTimeout`.

On either expiry the delegating task goes `FAILED` with an error naming the role and the configured
duration. **The peer is never canceled** (OD6 = let-run): no outbound `CancelTask` is wired anywhere
in this repo, and the spec explicitly requires leaving the peer running under its own supervisor.

### D11 — MCP schema branching: one exported constant, one early branch

```go
func buildInputSchema(kind string) *jsonschema.Schema {
    s := &jsonschema.Schema{
        Type:       "object",
        Properties: map[string]*jsonschema.Schema{bodyArg: bodySchema(kind)},
        Required:   []string{bodyArg},
    }
    if kind == port.KindDelegateTask {
        s.Properties[port.TargetArg] = targetSchema()
        s.Required = append(s.Required, port.TargetArg)
    }
    return s
}
```

Eight added lines; the tool-registration loop and the handler are untouched (the handler already
records the whole args map into `ActionIntent.Payload`).

| Alternative | Why rejected |
|---|---|
| A `delegation: true` flag in the `risk_policy` YAML entry | Puts an agent-visible tool-schema decision in the hands of whoever edits `company.yaml`, and the supervisor's routing switch would need the same flag threaded down — two sources of truth for one fact. |
| Naming convention (`delegate_*` prefix) | An accidentally-named future kind silently acquires a required `target` argument. Untestable as a boundary. |
| **Chosen**: one exported constant in `core/port` | `transport/mcp` and `core/supervisor` both already import `core/port`, so no cycle. The schema branch and the routing switch compare against the same symbol — a rename is a compile error. |

### D12 — Target addressing is by ROLE (OD2 resolved by spec)

The spec mandates role. Beyond matching policy vocabulary, role addressing is what makes **restart
recovery possible at all**: each agent's base URL is an ephemeral port that changes on every restart,
so a recovered watcher can only find its peer by re-resolving a stable key through the rebuilt
directory. A name- or URL-keyed wait would be unresolvable after a restart.

`company.yaml`'s new uniqueness constraint (one agent per role) is what makes the role key
unambiguous, and is enforced at `NewPeerDirectory`.

### D13 — Shipped policy: `delegate_task` is `risk: safe`

`allowed_roles: [ceo]` is mandated by the spec. `risk` is not. **Choice: `safe`** (→ `Permit`).

**Rationale**: delegation is an internal hand-off between the company's own agents; it has no
external blast radius by itself. Anything the peer subsequently does that *does* leave the company
(`telegram_send`) is independently classified against the peer's own role and escalates on its own
merits — that is exactly the chained-approval path this design builds. Shipping `risky` would put a
human approval in front of every internal hand-off, making the CEO useless as an autonomous
operator and making the chained path the common case rather than the edge case.

**Rejected**: `risk: risky`. Defensible for a first production rollout, and a one-line config change
for any operator who wants it — which is precisely why it should not be the shipped default.

---

## Data Flow

**Happy path — peer completes without escalating**

```
  CEO Execute goroutine                        engineer supervisor
  ─────────────────────                        ──────────────────
  RunTask → intent{delegate_task,              
            target:"engineer", body:"…"}
  Classify → Permit
  executeAction
    └─ Delegator.Delegate("engineer", body) ──────► /invoke SendMessage
         dir.BaseURL → resolve card                   (bearer + tenant)
         AttachSessionID + SendMessage                Execute → RunTask
                                    ◄────────────── COMPLETED + output text
    ◄─ DelegationResult{COMPLETED, "Done: X"}
  rec.Output = "Done: X"; rec.State = COMPLETED
```

**Chained approval — peer escalates**

```
  CEO Execute goroutine            watcher goroutine        engineer supervisor      human / UI
  ─────────────────────            ────────────────         ──────────────────       ──────────
  Delegate(...) ──────────────────────────────────────────► SendMessage
                                                            telegram_send → Escalate
                                   ◄──────────────────────── INPUT_REQUIRED (task P)
  DelegationResult{INPUT_REQUIRED, P}
  rec.AwaitingPeerRole = engineer
  rec.AwaitingPeerTaskID = P
  rec.AwaitingPeerDeadline = now+10m
  rec.State = INPUT_REQUIRED
  start watcher ──────────────────►│                                                 sees ONE
  yield INPUT_REQUIRED; RETURN     │ poll GetTask(P) ──────► INPUT_REQUIRED          control:
                                   │ poll GetTask(P) ──────►                    ◄──── approve P
                                   │                         resume → COMPLETED
                                   │ poll GetTask(P) ──────► COMPLETED + output
                                   │
                                   └─ Resumer.Resume(ceoTaskID)
  Execute (isResume) ─────────────────┘
  executeResume: rec.AwaitingPeerTaskID != ""
    └─ PeerTaskState("engineer", P) ────────────────────────► COMPLETED + output
  rec.Output = peer output; clear awaiting fields; COMPLETED
```

The delegating task's UI card shows a non-actionable `Awaiting engineer · P` badge throughout —
never an approve/reject pair. One human decision, one control.

---

## File Changes

| File | Action | Description |
|---|---|---|
| `core/port/delegator.go` | Create | `Delegator`, `TaskResumer`, `DelegationResult`, `KindDelegateTask`, `TargetArg`. |
| `core/port/provider.go` | Modify | Delete `SendMessage`, `SendMessageStream`, `ResolveAgent`, `StreamEvent`; update the contract doc comment. |
| `core/port/fake/fake_provider.go` | Modify | Delete the three methods, `SendMessageCall`, `MsgCalls`, `ReturnStream`, `ReturnAddress`, `SendMessageCallCount`. |
| `core/port/fake/fake_delegator.go` | Create | `fake.Delegator` — per-role scripted `Delegate`, sequenced `PeerTaskState`, recorded calls. |
| `core/port/fake/fake_resumer.go` | Create | `fake.Resumer` — records `Resume` calls; optional canned error. |
| `core/port/contract_test.go` | Modify | Drop A2A-method assertions; add a method-set assertion that `port.Provider` has exactly the five allowed methods. |
| `core/supervisor/store.go` | Modify | `TaskRecord` gains `AwaitingPeerRole`, `AwaitingPeerTaskID`, `AwaitingPeerDeadline` (all `omitempty`). |
| `core/supervisor/supervisor.go` | Modify | Delete `executeDelegation`, `roleOf`, the `PolicyEngine == nil` branch; `New` returns `(*Supervisor, error)`; add `Config.Delegator`, `Config.DelegateTimeout`, `Config.AwaitPeerTimeout`, `Config.PeerPollInterval`, `Config.Now`; `SetResumer`; `executeAction` → `(actionOutcome, error)` with `delegate_task` routing; awaiting-peer park + watcher; awaiting-peer branch first in `executeResume`; terminal `COMPLETED` event carries `rec.Output`; `extractTarget` helper; `filterAwaitingPeerTasks`; watcher restart in `RecoverOpenTasks`; watcher cancellation in `Shutdown`. |
| `core/supervisor/supervisor_test.go` | Modify | `New` now returns an error at every construction site. |
| `core/supervisor/integration_test.go` | Modify | `startSupervisor` supplies a `PolicyEngine`; delete `TestIntegration_CEODelegatesToWorkerOverRealWire` and its stale comment; add real-wire delegation, chained-approval, and restart-recovery tests. |
| `core/supervisor/store_test.go` | Modify | Awaiting-peer field round-trip + backward-compat with records lacking the fields. |
| `transport/a2a/directory.go` | Create | `PeerDirectory`, `ErrUnknownRole`, `ErrPeerNotRegistered`. |
| `transport/a2a/directory_test.go` | Create | Table-driven lookup, duplicate-role, rebind, concurrency (`-race`). |
| `transport/a2a/client.go` | Create | `Client` implementing `port.Delegator`: card resolution, `AttachSessionID` + credentials store, tenant, `Delegate`, `PeerTaskState`. |
| `transport/a2a/client_test.go` | Create | Real-server integration: card-before-send, bearer present, bearer wrong → rejected, tenant match, blocking-until-terminal, deadline → distinguishable timeout. |
| `transport/a2a/server_test.go` | Modify | Retarget `TestProviderFailureMarksFailed` to the `RunTask` failure path it actually proves. |
| `transport/mcp/tools.go` | Modify | `buildInputSchema` branches on `port.KindDelegateTask`; `targetSchema` helper; tool description mentions `target` for that kind. |
| `transport/mcp/tools_test.go` | Modify | Schema table: `delegate_task` → `[body, target]`; `telegram_send` → `[body]`. |
| `adapters/claudecode/adapter.go` | Modify | Delete three `errNotImplemented` stubs and the false comment. |
| `adapters/opencode/adapter.go` | Modify | Same. |
| `cmd/company/wire.go` | Modify | Build `PeerDirectory` before the agent loop (duplicate-role error); `Bind` after each `transa2a.New`; construct the A2A `Client`; inject `Config.Delegator` + timeouts; `SetResumer`; handle `supervisor.New`'s error; add `companyUIAdapter`. |
| `cmd/company/wire_test.go` | Modify | Duplicate-role materialize failure; directory bound for every agent. |
| `cmd/company/main.go` | Modify | UI backed by `companyUIAdapter`; `SetOnStateChange` on every runtime. |
| `ui/handler.go` | Modify | `TaskRecord` gains `Agent`, `AwaitingPeerRole`, `AwaitingPeerTaskID`; add `ErrAwaitingPeer` → 409. |
| `ui/handler_test.go` | Modify | Awaiting-peer task returns 409 on approve; `/api/tasks` exposes the marker. |
| `ui/app.js` | Modify | Non-actionable "Awaiting `<role>`" badge instead of approve/reject when `awaiting_peer_task_id` is set; show agent name. |
| `ui/style.css` | Modify | Badge style. |
| `company.yaml` | Modify | `delegate_task: {risk: safe, allowed_roles: [ceo]}`. |
| `agents/ceo.md` | Modify | When and how to delegate; that the outcome is unavailable this turn. |
| `agents/engineer.md` | Create | Minimal peer persona (OD4 resolved: ship it — an unprompted peer is the weakest link in the first end-to-end run). |
| `go.mod` | Modify | `golang.org/x/mod v0.35.0 // indirect` — must land in the PR that first imports `a2aclient`. |

---

## Testing Strategy

TDD is mandatory here. Every element below states its RED test and its double.

| Layer | What to test | Approach and double |
|---|---|---|
| Unit | `PeerDirectory` lookup, duplicate role, rebind, unknown vs not-bound | Table-driven, no doubles. Concurrency case: `Bind` and `BaseURL` from parallel goroutines under `-race`. |
| Unit | `buildInputSchema` per kind | Table-driven over `[delegate_task, telegram_send, arbitrary_kind]` asserting the exact `Required` slice. |
| Unit | `executeAction` routes `delegate_task` to the port, everything else to the gateway | `fake.Delegator` + `fake.Gateway`; assert `Gateway.Send` call count is 0 for delegation and unchanged for `telegram_send`. |
| Unit | Terminal/failed/timeout/unknown-role outcomes | `fake.Delegator` returning each canned `DelegationResult` / error; assert the persisted `TaskRecord` state, output, and that the error text names the role. |
| Unit | Awaiting-peer park writes the right record | `fake.Delegator` returning `INPUT_REQUIRED`; assert the three awaiting fields set, `PendingIntentKind` empty, state `INPUT_REQUIRED`. |
| Unit | Record-shape invariant | Table over both `INPUT_REQUIRED` shapes; assert exactly one field set is populated. |
| Unit | `executeResume` awaiting-peer branch takes precedence | `fake.Delegator` + `fake.Provider`; assert `RunTaskCallCount() == 1` for the whole chain (the "agent CLI never sees the result" requirement). |
| Unit | Watcher pokes exactly once, then exits | `fake.Delegator` with a sequenced `PeerTaskState` (`INPUT_REQUIRED, INPUT_REQUIRED, COMPLETED`) + `fake.Resumer`; injected 1 ms poll interval. |
| Unit | Deadline expiry drives FAILED naming role and duration | Injected `Config.Now` returning a controllable clock; no `time.Sleep`. |
| Unit | Loop guard | `delegate_task` intent from an `engineer`-role supervisor → `REJECTED`; separate test asserts the recorded payload keys are exactly `{target, body}` (no hop counter). |
| Unit | `port.Provider` method set | Reflection over the interface type asserting exactly `Complete, CompleteError, SendTask, RunTask, Capabilities`. Fails loudly if anyone re-adds a network method. |
| Unit | UI | Awaiting-peer task → `PostVerdict` 409; `/api/tasks` JSON carries `awaiting_peer_task_id`; `companyUIAdapter` routes a verdict to the owning runtime. |
| Integration | Client: card-before-send, bearer present, bearer wrong → rejected, tenant match, blocking, deadline | **Real `transa2a.Server` on a real loopback port, driven by a `fake.Provider`.** No CLI. A request-recording `http.RoundTripper` proves the well-known card was fetched before `/invoke` and that `Authorization: Bearer` was present. A session-less variant proves the `AttachSessionID` requirement is real. |
| Integration | End-to-end delegation over the real wire | Two real servers, both with `fake.Provider`s: the CEO's scripted to emit a `delegate_task` intent, the engineer's to return output. Assert engineer `RunTaskCallCount() > 0` and the CEO record's `Output` contains the engineer's text. Replaces the misnamed test. |
| Integration | Chained approval | Engineer's `fake.Provider` emits a `risky` `telegram_send`; assert both tasks `INPUT_REQUIRED`, exactly one has `AwaitingPeerTaskID`, then approve the *peer* and assert the CEO task auto-completes with the peer's output and **no** verdict was ever posted to it. |
| Integration | Restart recovery | Pre-seed a store with an awaiting-peer record, start the servers, assert the watcher re-establishes and resolves. Second case: role absent from `company.yaml` → task driven to `FAILED` naming the role. Third case: persisted deadline already past → immediate honest `FAILED`. |
| Integration | Timeout leaves the peer running | After the CEO task fails, `GetTask` on the peer still reports `INPUT_REQUIRED`. |

**Doubles inventory**

| Double | Location | Role |
|---|---|---|
| `fake.Delegator` | `core/port/fake/fake_delegator.go` | Per-role canned `Delegate` results and errors; `PeerTaskState` returns a scripted sequence so a test can drive "parked, parked, done" deterministically. Records every call. |
| `fake.Resumer` | `core/port/fake/fake_resumer.go` | Records `Resume(taskID, input)`; optional canned error to exercise the `ErrExecutionInProgress` collision. |
| `fake.Provider` (narrowed) | existing | Unchanged role — scripts `RunTask` output and action intents. |
| Real `transa2a.Server` + `fake.Provider` | integration tests | **The seam that keeps the hard parts testable without a live CLI.** The transport, interceptors, task store, and a2asrv execution manager are all real; only the CLI subprocess is faked. Every auth/tenant/polling/chaining claim in this design is proven against the real library. |
| Injected clock + poll interval | `supervisor.Config.Now`, `Config.PeerPollInterval` | No `time.Sleep` in any test; deadline and polling behavior are deterministic. |

**What cannot be tested without a live CLI**: nothing in this change. The provider boundary was
already faked; delegation adds no new subprocess dependency. All integration tests are gated on
`testing.Short()` because they bind real loopback ports, matching the existing repo convention.

---

## Threat Matrix

The matrix's five rows target shell/VCS/PR automation, none of which this change touches. Recorded
honestly rather than expanded:

| Boundary | Applicability | Reason |
|---|---|---|
| Documentation-like paths | **N/A** | No file classification or execution-by-extension anywhere in this change. |
| Git repository selection | **N/A** | No git invocation, no `cwd` authority change. |
| Commit state | **N/A** | No VCS automation. |
| Push state | **N/A** | No VCS automation. |
| PR commands | **N/A** | No PR automation. |

This change *does* introduce a routing and authorization boundary, which the generic matrix does not
cover. Its adversarial cases are carried as first-class requirements above, each with a named RED
test:

| Boundary | Adversarial case | Design response | RED test |
|---|---|---|---|
| Role → URL routing | undeclared role | `ErrUnknownRole`, task `FAILED` | `TestPeerDirectory_UnknownRole` |
| Role → URL routing | declared but unbound (startup window) | `ErrPeerNotRegistered`, transient for the watcher, fatal for a fresh `Delegate` | `TestPeerDirectory_NotYetBound`, `TestWatcher_RetriesNotRegisteredUntilDeadline` |
| Role → URL routing | duplicate role in `company.yaml` | materialize fails before any supervisor is ready | `TestMaterializeAgents_DuplicateRoleFailsBeforeAnyReady` |
| Outbound auth | missing session → header silently absent | `AttachSessionID` on every call | `TestClient_SessionlessRequestIsRejectedByPeer` |
| Outbound auth | wrong token | peer `authInterceptor` rejects; no task created | `TestClient_WrongTokenIsRejectedByPeer` |
| Tenant isolation | mismatched tenant | peer `tenantInterceptor` rejects | `TestClient_WrongTenantIsRejectedByPeer` |
| Capability escalation | non-`ceo` role delegates onward | existing `HardDeny` capability check | `TestDelegateTask_FromNonAllowedRoleIsHardDenied` |

---

## Migration / Rollout

No persisted format migration. The three new `TaskRecord` fields are `omitempty`; records written
before this change deserialize with zero values, which the invariant reads as "self-escalation or
ordinary task" — the pre-change meaning.

The feature is **dark until the final slice**: nothing delegates until `company.yaml` declares
`delegate_task`, because the policy engine fails closed on undeclared kinds
(`core/policy/engine.go:58-62`) and the MCP tool loop only registers declared kinds. Config-level
rollback is deleting that one YAML entry.

---

## PR Slicing

Delivery is `auto-chain`. Each slice is ≤400 authored changed lines and independently green on
`go build ./... && go test ./... -race`.

> **Scope decision**: this change ships slices **1–6 and 10** (~1670 lines, 7 PRs). Slices
> **7, 8 and 9** (~840 lines) are **DEFERRED** to the follow-up change
> `openspec/changes/agent-delegation-chained-approval/`, which depends on this change being
> merged first. Their rows and their governing decisions (D1–D5) are retained below and
> throughout this document on purpose — the design work is good and the follow-up change
> consumes it directly rather than re-deriving it.
>
> **In the follow-up, slice 9 MUST land before slice 7**: `cmd/company/main.go:67` binds the
> monitoring UI to `runtimes[0]` (the CEO) only, so a peer's `INPUT_REQUIRED` task is invisible
> and unapprovable; chained approval merged without UI aggregation could only ever time out.

| # | Slice | Est. lines | Independently green because |
|---|---|---|---|
| 1 | **Narrow `port.Provider`** — delete the three methods + `StreamEvent`, the six adapter stubs, `fake.Provider`'s A2A surface, and `executeDelegation` + `roleOf` + the `PolicyEngine == nil` branch (its only caller). Retarget `TestProviderFailureMarksFailed`. | ~290 (mostly deletion) | `wire.go:351` always sets `PolicyEngine`, so the removed branch was already unreachable — zero production behavior change. |
| 2 | **`supervisor.New` returns an error** on nil `PolicyEngine`. | ~120 (mostly test churn) | Split from #1 so the wide mechanical test churn does not hide the deletions. |
| 3 | **Peer directory** — `directory.go` + tests + construction/`Bind` in `materializeAgents` + duplicate-role materialize error. | ~220 | Nothing consumes it yet. |
| 4 | **A2A client** — `core/port/delegator.go`, `transport/a2a/client.go`, `go.mod` `golang.org/x/mod`, real-server integration tests. | ~380 | Not injected into any supervisor yet. **Largest slice — watch the forecast.** If it overruns, split `Delegate` from `PeerTaskState`. |
| 5 | **Output on the wire** — terminal `COMPLETED` event carries `rec.Output`. | ~60 | Tiny but load-bearing for #6; independently reviewable as a semantic change. |
| 6 | **Supervisor routing (synchronous half)** — `Config.Delegator`, `DelegateTimeout`, `actionOutcome`, terminal/failed/timeout/unknown-role handling, `fake.Delegator`. A non-terminal peer result fails with an explicit "peer escalated; chained approval not yet wired" error — an honest interim state, not a lie. | ~330 | Tests declare their own `PolicyConfig`; the shipped `company.yaml` is still silent. |
| 7 | **DEFERRED → `agent-delegation-chained-approval`.** **Chained approval** — awaiting-peer record fields, `TaskResumer` + `SetResumer`, the watcher, `executeResume` precedence, `AwaitPeerTimeout`, `fake.Resumer`, two-server integration test. | ~360 | Replaces #6's interim error path. Must land AFTER #9. |
| 8 | **DEFERRED → `agent-delegation-chained-approval`.** **Restart recovery** — `filterAwaitingPeerTasks`, watcher restart on the persisted deadline, transient-vs-fatal directory errors, honest-failure path. | ~200 | Depends on #7. |
| 9 | **DEFERRED → `agent-delegation-chained-approval`.** **UI aggregation** — `companyUIAdapter`, `ui.TaskRecord` fields, `ErrAwaitingPeer`, `app.js` badge, per-runtime `SetOnStateChange`. | ~280 | Prerequisite of #7 — without it the peer's approval control is unreachable. |
| 10 | **Turn it on** — `company.yaml` `delegate_task`, `agents/ceo.md`, `agents/engineer.md`, final wiring. | ~250 | The feature goes live only here. |

Slices 1+2 may merge if the test churn in #2 proves small; #5 may fold into #6. Do not merge #4 with
anything. Within this change, slice 10 follows slice 6 directly — slices 7, 8 and 9 are deferred and
are not a prerequisite of turning the feature on, because the shipped `company.yaml` grants no
risky action kind to a non-CEO role, so no peer can escalate.

---

## Open Questions

All four are resolved. None blocks `sdd-tasks`.

- [x] **The UI aggregation (D5, slice 9) is scope the proposal's affected-areas table does not
      list.** It is forced by the spec's "one human approval" requirement — without it the peer's
      approval control is unreachable and every chained delegation can only time out.
      **Resolved: deferred to `agent-delegation-chained-approval` together with slices 7 and 8.**
      The rationale for deferring rather than dropping: the shipped `company.yaml` grants
      `telegram_send` to `ceo` only, so a peer attempting a risky action is HardDenied and never
      escalates — the chained path is unreachable with the shipped config. In the follow-up,
      slice 9 must land **before** slice 7, so a chained approval is never merged without the
      control that approves it.
- [x] `PeerPollInterval` default of 5s is a guess. **Resolved: deferred.** The poller exists only
      to serve the awaiting-peer watch (slices 7-8), which moved to the follow-up. Pick the number
      there, against a real approval-latency observation rather than a guess.
- [x] Nothing caps the number of concurrent watcher goroutines. **Resolved: deferred** with the
      watcher itself (slices 7-8). This change starts no watcher goroutines, so the question does
      not arise here. The follow-up must not inherit the cap as "already accepted" — it is still
      open there.
- [x] `agents/engineer.md` (OD4) is proposed as in-scope. **Resolved: confirmed in scope**, slice
      10. The first end-to-end run must not have an unprompted peer: a raw CLI with no company
      context is exactly the failure mode that produced the "CEO escalated in prose instead of
      calling the tool" bug in the previous change.

## Open Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| `AttachSessionID` is easy to omit and fails **silently** (no header, not an error) | High | A dedicated RED test asserts a session-less call is rejected by the peer. The requirement is documented inline at the call site. |
| A peer task that completed *before* a restart is gone from the peer's fresh in-memory `a2asrv` store; a delegator awaiting it fails honestly rather than recovering the result | Medium | Spec-compliant (explicit failure naming the peer), but a real data-loss window. Persisting the a2asrv task store is a follow-up, not this change. |
| Watcher `Resume` and a human verdict racing produce `ErrExecutionInProgress` on one of them | Low | `PostVerdict` rejects awaiting-peer tasks up front, and the awaiting-peer resume branch is idempotent (it re-queries). The window is narrow but real. |
| Slice 4 (A2A client) overruns 400 lines | Medium | Pre-planned split point: `Delegate` first, `PeerTaskState` + polling helper second. |
| `supervisor.Config` grows five new fields (`Delegator`, two timeouts, poll interval, clock) | Medium | All zero-valued to working defaults; only `Delegator` is required, and only for roles granted `delegate_task`. Revisit as a `DelegationConfig` sub-struct if it grows again. |
| Terminal-event output change (D9) alters every completed task's A2A event, not just delegated ones | Low | Strictly additive; no existing consumer reads `Status.Message` on `COMPLETED`. Covered by a regression test on the non-delegated path. |
