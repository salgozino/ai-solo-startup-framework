# Delta for Agent Supervisor (Awaiting-Peer Recovery)

> **Target note**: This delta targets the "Crash and Restart Recovery" requirement of the
> `agent-supervisor` spec. At the time of writing that requirement lives in the PENDING,
> UNARCHIVED spec
> `openspec/changes/agent-startup-framework-foundation/specs/agent-supervisor/spec.md`;
> `openspec/specs/agent-supervisor/spec.md` does not yet exist. `sdd-archive` MUST merge the
> `MODIFIED Requirements` block below into whichever of the two now holds that requirement.
>
> `agent-delegation-over-a2a` also carries an `agent-supervisor` delta, but only an
> `ADDED Requirements` block — it does not touch "Crash and Restart Recovery". The two deltas
> therefore do not collide, and both MUST be merged into the same spec when all changes archive.
>
> The block below was authored and reviewed as part of `agent-delegation-over-a2a` and MOVED here
> verbatim when that change was scoped down to design slices 1–6 and 10. It is unchanged in
> wording.

## MODIFIED Requirements

### Requirement: Crash and Restart Recovery

On process restart, the supervisor MUST reload persisted task state keyed by the full A2A
address, enter `RECOVERING`, replay open tasks, and re-invoke the provider from the last
persisted task state rather than resuming a dead child process in place. When a reloaded task
is parked `INPUT_REQUIRED` specifically because it is awaiting a peer's terminal state (a
chained delegation wait, not an ordinary self-escalation), recovery MUST re-establish that wait
— by re-watching or re-querying the peer's task through the delegation port or A2A client — or
drive the task to an explicit, honestly-labeled failure if the peer can no longer be reached.
Recovery MUST NOT silently resubmit a delegation-wait task as a brand-new task, and MUST NOT
leave it permanently `INPUT_REQUIRED` with no mechanism that could ever resolve it.

(Previously: recovery covered only ordinary `WORKING` re-submission and ordinary
`INPUT_REQUIRED` re-registration; it said nothing about a delegation-specific await state,
because no such state existed before this change.)

#### Scenario: Restart after a mid-task crash

- GIVEN a supervisor crashes while a task is `WORKING`
- WHEN the supervisor process restarts
- THEN it enters `RECOVERING`, reloads that task from persisted state keyed by the full address,
  and re-invokes the provider for it — it does not attempt to resume the dead child process

#### Scenario: Restart preserves a parked INPUT_REQUIRED task

- GIVEN a task is parked in `INPUT_REQUIRED` when the supervisor process is restarted
- WHEN the supervisor comes back up
- THEN the task remains `INPUT_REQUIRED` and resumable by a new approval message — it is not
  lost or reset

#### Scenario: Restart re-establishes an in-flight delegation wait

- GIVEN a delegating task is parked `INPUT_REQUIRED` awaiting a peer's terminal state when the
  supervisor process restarts
- WHEN the supervisor recovers open tasks
- THEN it re-establishes a means of learning the peer's terminal state for that task, so the
  task can still auto-resume once the peer completes — it does not resubmit the task as fresh
  and does not leave it permanently unresolvable

#### Scenario: Restart fails a delegation wait honestly when the peer can no longer be reached

- GIVEN a delegating task is parked `INPUT_REQUIRED` awaiting a peer, and after restart the peer
  can no longer be reached (for example, its role is no longer registered)
- WHEN the supervisor attempts to re-establish the wait
- THEN the task is driven to an explicit `FAILED` state naming the unreachable peer, rather than
  remaining `INPUT_REQUIRED` indefinitely with no path to resolution
