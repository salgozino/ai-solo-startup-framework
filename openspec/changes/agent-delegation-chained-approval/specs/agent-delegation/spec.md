# Delta for Agent Delegation (Chained Approval)

> **Target note**: This delta targets the PENDING, UNARCHIVED spec
> `openspec/changes/agent-delegation-over-a2a/specs/agent-delegation/spec.md`.
> `openspec/specs/agent-delegation/spec.md` does not yet exist — the `agent-delegation` capability
> is created by `agent-delegation-over-a2a`, which MUST be merged and archived before this change.
> `sdd-archive` MUST append the `ADDED Requirements` below to that spec, and MUST apply the
> `REMOVED Requirements` block, when both changes archive.
>
> The three `ADDED Requirements` below were authored and reviewed as part of
> `agent-delegation-over-a2a` and MOVED here verbatim when that change was scoped down to design
> slices 1–6 and 10. They are unchanged in wording.

## ADDED Requirements

### Requirement: A Peer Escalation Chains the Delegating Task Into a Single Human Approval

When the peer's own task escalates to `INPUT_REQUIRED` (for example, the peer wants to send a
Telegram message), the delegating task MUST also transition to `INPUT_REQUIRED`, parked
awaiting the peer. The human MUST approve or reject exactly once, on the peer's task — never on
the delegating task, for the same underlying decision. When the peer's task subsequently
reaches any terminal state (`COMPLETED`, `FAILED`, `REJECTED`, or `CANCELED`), the delegating
task MUST automatically resume without further human input and complete carrying the peer's
terminal output (or fail, per the result-copy requirement above, when the peer's terminal state
was not `COMPLETED`).

#### Scenario: A peer escalation parks the delegating task, not just the peer

- GIVEN the CEO delegates to the `engineer` role, and the engineer's own task escalates to
  `INPUT_REQUIRED` for a risky action
- WHEN the escalation occurs
- THEN both the peer's task and the CEO's delegating task are `INPUT_REQUIRED`

#### Scenario: One human approval resolves both tasks

- GIVEN both the peer's task and the CEO's delegating task are `INPUT_REQUIRED` from a chained
  escalation
- WHEN the human approves the peer's task and the peer's task subsequently reaches `COMPLETED`
- THEN the CEO's delegating task auto-resumes without any separate human approval action on the
  CEO's task, and completes carrying the peer's output

#### Scenario: The human is never asked twice for one decision

- GIVEN a chained escalation as above
- WHEN the monitoring UI or any human-facing surface is inspected before the peer resolves
- THEN it presents exactly one pending approval for this decision — on the peer's task — and
  never a second pending approval on the delegating task

### Requirement: The Delegating Task's Awaiting-Peer Wait Is Distinguishable From an Ordinary Self-Escalation

A delegating task parked in `INPUT_REQUIRED` because it is awaiting a peer (chained approval)
MUST be distinguishable, both in its persisted record and in any SSE/UI surface derived from
it, from a task parked in `INPUT_REQUIRED` because it escalated its own action intent directly.
The distinction MUST be sufficient for a monitoring UI to never render two pending-approval
controls for what is, from the human's perspective, a single decision.

#### Scenario: The stored record marks an awaiting-peer wait differently from a self-escalation

- GIVEN a CEO task is `INPUT_REQUIRED` because its own `telegram_send` intent escalated
- AND a different CEO task is `INPUT_REQUIRED` because a delegated peer's task escalated
- WHEN the two persisted task records are inspected
- THEN they carry distinguishable markers — the delegating task's record identifies itself as
  "awaiting a peer" and names the peer's task, while the self-escalated task's record
  identifies itself as "awaiting your approval"

#### Scenario: The monitoring surface never shows a duplicate approval control

- GIVEN a chained escalation with the CEO's delegating task `INPUT_REQUIRED` awaiting its peer
- WHEN the monitoring UI renders pending approvals across both tasks
- THEN it renders exactly one actionable approve/reject control, attached to the peer's task,
  and renders the delegating task's `INPUT_REQUIRED` state as a non-actionable "awaiting peer"
  indicator

### Requirement: A Delegating Task Parked Awaiting a Peer Recovers Its Wait After a Restart

If the process restarts while a delegating task is parked `INPUT_REQUIRED` awaiting a peer, the
supervisor's restart recovery MUST re-establish that wait rather than silently dropping it,
silently treating it as a fresh task, or leaving it permanently stuck with no path to
resolution. Recovery MUST result in one of: the delegating task resuming its wait for the
peer's terminal state (by re-watching or re-querying the peer's task through the delegation
port or A2A client), or the delegating task being driven to an explicit, honestly-labeled
failure if the peer can no longer be reached — never a nil-pointer panic and never a wait that
can no longer ever resolve.

#### Scenario: Restart re-establishes an in-flight delegation wait

- GIVEN a CEO task is `INPUT_REQUIRED` awaiting a peer when the process restarts
- WHEN the supervisor recovers open tasks on startup
- THEN the delegating task remains resumable and the supervisor re-establishes a means of
  learning the peer's terminal state, so that when the peer later reaches a terminal state the
  delegating task still auto-resumes per the chained-approval requirement

#### Scenario: An unreachable peer after restart fails honestly rather than hanging forever

- GIVEN a CEO task is parked awaiting a peer whose supervisor is no longer reachable after
  restart
- WHEN recovery cannot re-establish the wait
- THEN the delegating task is driven to an explicit failure naming the peer, rather than
  remaining `INPUT_REQUIRED` with no mechanism that could ever resolve it

## REMOVED Requirements

### Requirement: A Non-Terminal Peer Result Fails the Delegating Task With an Explicit Not-Yet-Wired Error

**Reason**: superseded by "A Peer Escalation Chains the Delegating Task Into a Single Human
Approval" above. That requirement was `agent-delegation-over-a2a`'s honest interim behavior for
design slice 6: with chained approval unimplemented, a peer escalation had to fail loudly rather
than hang or pretend to succeed. Once chained approval lands, a peer escalation is a parked wait,
not a failure, so the interim requirement MUST be removed rather than left to contradict the new
one.

**Migration**: none for persisted data. Any test asserting the interim failure error is replaced
by the chained-approval tests; the delegating task's `INPUT_REQUIRED` park supersedes its
`FAILED` transition for the peer-escalation case only. Every other delegation failure path
(peer `FAILED`, timeout, unregistered role) is unchanged.

**Agent roles**: `ceo` parks awaiting a peer; `engineer` (or any delegated role) is the escalating
peer. **Protocol**: A2A `GetTask` polling observes the peer's state; the existing A2A resume path
(`SendMessage` with the original `TaskID`) resumes the delegating task.
