# Agent Delegation Specification

## Purpose

The CEO agent (or any role granted the new action kind) can hand a task to a peer agent,
addressed by role, and have the delegating task's own record carry the peer's terminal result.
Delegation reuses the existing action-intent classification pipeline (`Classify` → `HardDeny` /
`Escalate` / `Permit`) as its sole execution path and its sole recursion guard. By the time a
delegation intent is classified, the delegating agent's CLI process has already exited — as with
every other action intent — so continuity between "delegate" and "peer answered" is carried
entirely by the delegating task's persisted record, never by an agent-to-agent conversation.

## Requirements

### Requirement: Delegation Is a Policy-Classified Action Kind Addressed by Role

Delegation MUST be exposed as an action kind named `delegate_task`, declared in `risk_policy`
like any other action kind, and classified through the same `Classify` → `HardDeny` /
`Escalate` / `Permit` pipeline used for every other action kind. There MUST NOT be a second
execution path for delegation outside this pipeline. The delegation target MUST be identified
by agent ROLE (for example `engineer`), never by agent name; an intent's recorded target value
MUST be a role string, not the name of a specific configured agent.

#### Scenario: A Permitted delegation intent is recorded with a role target

- GIVEN a `ceo` agent calls the `delegate_task` MCP tool with `target: "engineer"` and a
  message body
- WHEN the MCP server records the intent
- THEN the recorded `ActionIntent` has `Kind: "delegate_task"` and its payload's target value
  is the role string `"engineer"`, not any agent's configured name

#### Scenario: Delegation has no execution path outside policy classification

- GIVEN a `delegate_task` intent has been recorded for a task
- WHEN the supervisor processes the task's action intents
- THEN the intent is classified via the same `Engine.Classify` call used for every other
  action kind, and no separate delegation-specific classification or execution branch exists

### Requirement: The Delegation Port Blocks Until the Peer Task Is Terminal

A Permitted `delegate_task` intent MUST be executed through a narrow delegation port exposing
exactly one delegation operation: send the intent's body to the role-addressed peer and block
the caller until the peer's task reaches a terminal A2A `TaskState`, or the delegation times out
(see the timeout requirement below). Streaming delegation is out of scope; only blocking
delegation is supported by this change.

#### Scenario: A Permitted delegation blocks the supervisor's task goroutine

- GIVEN a `delegate_task` intent is classified `Permit`
- WHEN the supervisor executes the action
- THEN it calls the delegation port's blocking operation and does not proceed to complete the
  delegating task until that call returns

#### Scenario: The delegation port is not part of `port.Provider`

- GIVEN the framework's port definitions
- WHEN a developer looks for the delegation operation
- THEN it is declared on a dedicated, narrow port distinct from `port.Provider`, which
  continues to mean "run a CLI subprocess and report intents"

### Requirement: The Peer's Terminal Result Is Copied Into the Delegating Task's Output

When the peer's task reaches `COMPLETED`, its output text MUST be copied verbatim into the
delegating task's own output, and the delegating task MUST then transition to `COMPLETED`.

#### Scenario: A completed peer delegation completes the delegating task with the peer's output

- GIVEN the CEO delegates to the `engineer` role and the peer task reaches `COMPLETED` with
  output `"Done: implemented X"`
- WHEN the delegation port returns
- THEN the CEO's task output contains `"Done: implemented X"` and the CEO's task transitions
  to `COMPLETED`

#### Scenario: A failed peer delegation fails the delegating task

- GIVEN the CEO delegates to the `engineer` role and the peer task reaches `FAILED`
- WHEN the delegation port returns
- THEN the CEO's task transitions to `FAILED` with an error that names the peer role and
  references the peer's failure, and no output is fabricated on the CEO's behalf

### Requirement: The Delegating Agent Process Never Observes the Peer's Result

By the time a `delegate_task` intent is classified, the delegating agent's CLI process has
already exited (as with every other action intent). Delegation MUST NOT be modeled,
implemented, or tested as a conversation in which the delegating agent's CLI process receives,
reads, or reacts to the peer's output. Only the delegating task's persisted record and its
terminal A2A events carry the peer's result; the delegating agent's own turn was already over
before the peer was ever contacted.

#### Scenario: No second invocation of the delegating agent's CLI occurs

- GIVEN a CEO task delegates to the `engineer` role and the peer eventually completes
- WHEN the delegation resolves
- THEN the CEO's provider (CLI subprocess) is invoked exactly once for that task — never again
  to "receive" or "react to" the peer's output — and the peer's output is written only to the
  task record

### Requirement: A Non-Terminal Peer Result Fails the Delegating Task With an Explicit Not-Yet-Wired Error

When the delegation port reports the peer's task in a NON-TERMINAL state — that is, the peer
escalated to `INPUT_REQUIRED` and awaits its own human verdict — the delegating task MUST
transition to `FAILED` with an error that explicitly states both that the peer escalated and
that chained approval is not yet wired, and that names the peer's role and the peer's task ID.
The delegating task MUST NOT hang or remain in a state nothing can resolve, MUST NOT transition
to `COMPLETED`, and MUST NOT fabricate output or otherwise present the delegation as having
succeeded. The peer's own task MUST be left running under its own supervisor, exactly as on a
delegation timeout.

This is a deliberate, honest interim behavior. Chained approval — parking the delegating task
until a single human approval on the peer's task resolves both — is specified by the follow-up
change `agent-delegation-chained-approval`, which supersedes this requirement.

#### Scenario: A peer that escalates fails the delegating task with an explicit not-yet-wired error

- GIVEN the CEO delegates to the `engineer` role, and the engineer's own task escalates to
  `INPUT_REQUIRED` rather than reaching a terminal state
- WHEN the delegation port returns that non-terminal result
- THEN the CEO's delegating task transitions to `FAILED` with an error stating that the peer
  escalated and that chained approval is not yet wired

#### Scenario: The error names the peer's role and the peer's task so the escalation stays traceable

- GIVEN a delegation fails because the peer escalated as above
- WHEN the delegating task's error text is inspected
- THEN it contains the peer's role `"engineer"` and the peer's A2A task ID, so a human can find
  the peer's pending approval

#### Scenario: A peer escalation neither hangs nor silently completes the delegating task

- GIVEN a delegation whose peer escalates and never reaches a terminal state on its own
- WHEN the delegation resolves
- THEN the delegating task reaches `FAILED` without waiting for the delegation timeout to
  expire, its output does not contain any fabricated peer result, and it never reaches
  `COMPLETED`

#### Scenario: The peer's task keeps running after the delegating task fails

- GIVEN a delegating task failed because its peer escalated
- WHEN the peer's task state is inspected immediately afterwards
- THEN the peer's task is still `INPUT_REQUIRED` under its own supervisor — not canceled,
  rejected, or altered as a side effect of the delegator's failure

### Requirement: Delegation Fails the Task on Timeout, Leaving the Peer Running

The delegation port's blocking operation MUST enforce a timeout, defaulting to 10 minutes and
configurable at the composition root. On expiry, the delegating task MUST transition to
`FAILED` with an error naming the peer's role and the configured timeout. The peer's own task
MUST be left running under its own supervisor; delegation timeout MUST NOT cancel, interrupt,
or otherwise affect the peer's task.

#### Scenario: A hung peer fails the delegating task within the timeout

- GIVEN the CEO delegates to the `engineer` role with the default 10-minute timeout, and the
  peer's task never reaches a terminal state
- WHEN 10 minutes elapse
- THEN the CEO's delegating task transitions to `FAILED` with an error that names `engineer`
  and the 10-minute timeout

#### Scenario: The peer's task is not canceled on the delegator's timeout

- GIVEN a delegation times out as above
- WHEN the peer's task state is inspected immediately after the delegating task fails
- THEN the peer's task is unaffected — still owned and driven by its own supervisor, not
  canceled or altered as a side effect of the delegator's timeout

### Requirement: Delegating to an Unregistered Role Fails the Task Explicitly

When a `delegate_task` intent's target role has no live, bound peer (unknown role, or a role
not yet registered because its server has not finished binding), the delegation port MUST
return an explicit, named error, and the delegating task MUST transition to `FAILED` with that
error — never a panic, and never a silent hang.

#### Scenario: An unknown target role fails the task with a clear error

- GIVEN a `delegate_task` intent targets role `"designer"`, which no agent in `company.yaml`
  declares
- WHEN the delegation port attempts to look up the peer
- THEN the delegating task transitions to `FAILED` with an error naming `"designer"` as an
  unregistered role, and no panic occurs

#### Scenario: A not-yet-bound target role fails the task rather than reading a nil map

- GIVEN a `delegate_task` intent targets role `"engineer"`, and the `engineer` agent's server
  has not yet finished binding when the delegation is attempted
- WHEN the delegation port attempts to look up the peer
- THEN the delegating task transitions to `FAILED` with an explicit "not yet registered" error
  naming `"engineer"` — never a nil-map read, never a panic

### Requirement: Delegation's Only Loop Guard Is the Existing Role-Capability Check

A peer agent that is not in `delegate_task`'s `allowed_roles` MUST be prevented from delegating
onward by the same capability check every other action kind already uses (`HardDeny` before any
risk evaluation). No additional recursion guard (hop counter, delegation-depth field, or
similar) MAY be introduced by this change; the shipped `company.yaml`'s `allowed_roles: [ceo]`
on `delegate_task` is the entire loop-prevention mechanism.

#### Scenario: A peer role attempting to delegate onward is hard-denied

- GIVEN the `engineer` role is not in `delegate_task`'s `allowed_roles`
- WHEN an `engineer`-role agent's task emits a `delegate_task` intent
- THEN the policy engine's existing capability check hard-denies it, the task is driven to
  `REJECTED`, and no delegation occurs — with no hop-counter or delegation-depth state involved
  anywhere in the decision

#### Scenario: No hop-counter field exists in the delegation payload

- GIVEN the `delegate_task` intent's recorded payload
- WHEN its shape is inspected
- THEN it carries only the target role and the message body — no hop count, depth, or
  chain-tracking field of any kind

**Agent roles**: `ceo` issues delegation by default (per the shipped `company.yaml`); any role
granted `delegate_task` in `risk_policy` may delegate. `engineer` and any other role receives
delegation. **Protocol**: MCP `tools/call` records the intent; the existing policy pipeline
classifies it; A2A JSON-RPC carries the blocking delegation call to the peer.
