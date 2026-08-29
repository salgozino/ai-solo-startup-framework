# Agent Supervisor Specification

## Purpose

The supervisor is the long-lived process that owns a single agent's identity: its A2A Server
endpoint, Agent Card, task queue, state machine, and memory. It invokes the provider ephemerally
per task and never exposes provider internals on the wire.

## Requirements

### Requirement: Supervisor Lifecycle States

The supervisor MUST progress through the states `STARTING → IDLE ⇄ WORKING`, `IDLE → DRAINING →
STOPPED`, and `crash → RECOVERING → IDLE`, for any agent role (`ceo`, `engineer`, or others
declared in `company.yaml`).

#### Scenario: Supervisor reaches IDLE after startup

- GIVEN a supervisor process has just started for an agent
- WHEN it finishes registering its A2A endpoint and Agent Card
- THEN it transitions to `IDLE` before accepting its first task

#### Scenario: Supervisor returns to IDLE after task completion

- GIVEN a supervisor is `WORKING` on a task
- WHEN the provider invocation for that task finishes
- THEN the supervisor returns to `IDLE` if its queue is empty

### Requirement: IDLE Is an Observable State

The supervisor MUST expose `IDLE` as a state distinguishable from `WORKING`: server serving, task
queue empty, no provider process running.

#### Scenario: Monitoring UI observes a genuinely idle agent

- GIVEN a supervisor has no queued tasks and no running provider child process
- WHEN the monitoring UI queries or receives a push update for that agent
- THEN the reported state is `IDLE`, not `WORKING` or any transitional label

### Requirement: Task State Progression Follows A2A TaskState

Each task owned by a supervisor MUST progress through A2A `TaskState` values: `SUBMITTED` when
queued, `WORKING` while the provider runs, `INPUT_REQUIRED` while awaiting human approval,
`COMPLETED` on success, `FAILED` on provider error, and `REJECTED` or `CANCELED` on policy
hard-deny or cancellation.

#### Scenario: Full escalation cycle traversal

- GIVEN a CEO-role agent's task requires a risky, permitted action
- WHEN the task is submitted, processed, escalated, approved, and completed
- THEN the task's recorded states are exactly `SUBMITTED → WORKING → INPUT_REQUIRED → WORKING →
  COMPLETED`

#### Scenario: Provider failure marks the task FAILED, not silently dropped

- GIVEN a supervisor's provider child process exits non-zero for a task
- WHEN the supervisor processes the exit
- THEN the task transitions to `FAILED` and is never left in `WORKING`

### Requirement: Crash and Restart Recovery

On process restart, the supervisor MUST reload persisted task state keyed by the full A2A
address, enter `RECOVERING`, replay open tasks, and re-invoke the provider from the last
persisted task state rather than resuming a dead child process in place.

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

### Requirement: Bounded Context Assembly

The supervisor MUST assemble per-task context (task input, minimal role-memory slice, prior
resolutions) fresh per invocation, capped by a declared context budget; when the assembled
context exceeds the budget, the supervisor MUST truncate the oldest content first and mark the
truncation, never truncating silently.

#### Scenario: Context within budget passes through unmarked

- GIVEN assembled context for a task is within the declared context budget
- WHEN the supervisor invokes the provider
- THEN the context is passed through without a truncation marker

#### Scenario: Oversized context is truncated with a visible marker

- GIVEN assembled context for a task exceeds the declared context budget
- WHEN the supervisor prepares the invocation
- THEN the oldest content is dropped first and the resulting context carries an explicit
  truncation marker

### Requirement: One Supervisor Owns One Agent Identity

Each supervisor MUST own exactly one agent's identity: one A2A endpoint, one Agent Card, one task
queue, keyed by that agent's full address (`agent-name/tenant`).

#### Scenario: Two agents in one company never share a queue

- GIVEN a `company.yaml` declares a `ceo` and an `engineer` agent under the same tenant
- WHEN both supervisors start
- THEN each has its own task queue and endpoint, and no task submitted to one appears in the
  other's queue
