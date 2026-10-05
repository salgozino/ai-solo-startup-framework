# Delta for monitoring-ui

## MODIFIED Requirements

### Requirement: State Updates Reach the UI Without Polling

The monitoring UI MUST reflect task state transitions as they happen, consuming the
supervisors' push notification stream rather than requiring the human to manually refresh.
The supervisor MUST call an `OnStateChange` callback after every FSM state transition and
after every task-state persistence (`Store.Save`). The callback MUST be nil-safe: if no
callback is registered, the supervisor MUST continue operating normally without error.

(Previously: requirement stated the UI consumes a push stream but did not specify the
supervisor-side mechanism driving that stream — the callback was absent, so SSE clients
received only keep-alive pings and no actual state events.)

#### Scenario: An escalation appears live without a manual refresh

- GIVEN a human has the monitoring UI open with no pending escalations visible
- WHEN a task transitions to `INPUT_REQUIRED` on a watched supervisor
- THEN the escalation appears in the UI without the human refreshing the page

#### Scenario: State change triggers an SSE event

- GIVEN the supervisor's `OnStateChange` callback is wired to `UIHandler.Broadcast`
- WHEN a task-state transition is persisted via `Store.Save`
- THEN `UIHandler.Broadcast` is called with `sup.Status()` and a `state` SSE event is
  delivered to all connected `/api/events` clients

#### Scenario: A single transition emits exactly one broadcast

- GIVEN a single FSM transition occurs (e.g., WORKING → IDLE)
- WHEN `OnStateChange` fires
- THEN exactly one call to `UIHandler.Broadcast` is made — not one per connected client

#### Scenario: Nil callback does not crash the supervisor

- GIVEN the supervisor is started without a registered `OnStateChange` callback
- WHEN a task state transitions
- THEN the supervisor continues operating without error and no SSE event is attempted
