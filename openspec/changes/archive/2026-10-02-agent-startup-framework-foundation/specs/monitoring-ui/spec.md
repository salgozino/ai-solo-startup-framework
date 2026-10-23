# Monitoring UI Specification

## Purpose

The monitoring UI is a read-only observation surface plus an approve/reject control on pending
escalations. It exists so a human can see agent and task state, and act on risk escalations,
without gaining any control that would bypass the CEO-only interaction rule.

## Requirements

### Requirement: Agent and Task State Is Displayed Read-Only

The monitoring UI MUST display the current state of every agent and task in the materialized
company (supervisor lifecycle state, and each task's `TaskState`) without allowing that display
to be edited directly.

#### Scenario: A viewer sees current state for all agents

- GIVEN a company with a `ceo` and an `engineer` agent is materialized and running
- WHEN a human opens the monitoring UI
- THEN they see the current supervisor state and task states for both agents

### Requirement: Approve/Reject Is Available Only for Pending Escalations

The monitoring UI MUST offer an approve/reject control for a task only while that task is in
`INPUT_REQUIRED`; it MUST NOT offer approve/reject for tasks in any other state.

#### Scenario: A completed task offers no approve/reject control

- GIVEN a task has already reached `COMPLETED`
- WHEN the human views that task in the monitoring UI
- THEN no approve/reject control is presented for it

#### Scenario: A pending escalation offers approve/reject

- GIVEN a task is currently `INPUT_REQUIRED`
- WHEN the human views that task in the monitoring UI
- THEN an approve/reject control is presented for it

### Requirement: The UI Cannot Pause, Kill, Reassign, or Command an Agent

The monitoring UI MUST NOT expose any control to pause, kill, reassign, or otherwise command an
agent's task or process; its only write action is recording an approve or reject verdict on a
pending escalation.

#### Scenario: No control exists to stop a running agent

- GIVEN an `engineer` agent's supervisor is `WORKING` on a task
- WHEN a human interacts with the monitoring UI for that agent
- THEN no control is available to pause, kill, or reassign that task — the only write action
  anywhere in the UI is an approve/reject verdict on a pending escalation

### Requirement: State Updates Reach the UI Without Polling

The monitoring UI MUST reflect task state transitions as they happen, consuming the
supervisors' push notification stream rather than requiring the human to manually refresh.

#### Scenario: An escalation appears live without a manual refresh

- GIVEN a human has the monitoring UI open with no pending escalations visible
- WHEN a task transitions to `INPUT_REQUIRED` on a watched supervisor
- THEN the escalation appears in the UI without the human refreshing the page
