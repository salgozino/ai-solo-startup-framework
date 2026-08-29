# Approval Flow Specification

## Purpose

Risk escalations pause a task, wait for a human verdict, and resume it. This spec covers the
`INPUT_REQUIRED` task-state semantics for escalation and resume, and the observable effect of
approval versus rejection. `AUTH_REQUIRED` is out of scope for v1 escalation.

## Requirements

### Requirement: Escalation Uses INPUT_REQUIRED, Never AUTH_REQUIRED

When the risk policy escalates an action for human approval, the owning task MUST transition to
`INPUT_REQUIRED`. The task MUST NOT transition to `AUTH_REQUIRED` for this purpose.

#### Scenario: A risky, permitted action transitions to INPUT_REQUIRED

- GIVEN a `ceo` agent's `telegram_send` intent is permitted and classified risky
- WHEN the policy engine escalates it
- THEN the task's state is `INPUT_REQUIRED`, and at no point during this escalation does the
  task enter `AUTH_REQUIRED`

### Requirement: An Escalated Task Survives a Process Restart

A task parked in `INPUT_REQUIRED` MUST remain resumable after the owning supervisor process
restarts; the escalation MUST NOT depend on any in-memory or in-process state that a restart
would destroy.

#### Scenario: Escalated task is still resumable after a restart

- GIVEN a task is parked in `INPUT_REQUIRED` awaiting a human verdict
- WHEN the supervisor process is restarted before a verdict is recorded
- THEN the task remains `INPUT_REQUIRED` after restart and can still be resumed by a subsequent
  approval

### Requirement: Resume Is a New Message Carrying the Same Task ID

Resuming an `INPUT_REQUIRED` task MUST be expressed as a new message carrying that task's
existing task ID; the framework MUST distinguish this as a resume, not a new task.

#### Scenario: An approval resumes the correct parked task

- GIVEN a task is `INPUT_REQUIRED` with a known task ID
- WHEN the human's approval verdict is delivered as a new message carrying that task ID
- THEN the supervisor recognizes it as a resume of that specific task, not a new task submission

### Requirement: Human Approval Results in Exactly One Effect

When the human approves a pending `INPUT_REQUIRED` escalation, the associated action MUST
execute exactly once, and the task MUST resume to `WORKING` and proceed toward `COMPLETED`.

#### Scenario: Approval sends the message exactly once

- GIVEN a `ceo` agent's `telegram_send` escalation is pending in `INPUT_REQUIRED`
- WHEN the human approves it via the monitoring UI
- THEN exactly one Telegram send occurs, the task resumes to `WORKING`, and it subsequently
  reaches `COMPLETED`

### Requirement: Human Rejection Results in Zero Effect

When the human rejects a pending `INPUT_REQUIRED` escalation, the associated action MUST NOT
execute, and the task MUST be driven to `REJECTED`.

#### Scenario: Rejection prevents the send entirely

- GIVEN a `ceo` agent's `telegram_send` escalation is pending in `INPUT_REQUIRED`
- WHEN the human rejects it via the monitoring UI
- THEN zero Telegram sends occur and the task transitions to `REJECTED`

### Requirement: Escalation Payload Is Versioned and Validated on Both Ends

The approval-request payload carried in the task's status MUST include a version marker, and
both the emitting and consuming ends MUST validate it before acting on its contents.

#### Scenario: An unrecognized payload version is not blindly trusted

- GIVEN an approval-request payload arrives carrying a version the receiving end does not
  recognize
- WHEN the receiving end processes it
- THEN it rejects or flags the payload as invalid rather than acting on unvalidated contents
