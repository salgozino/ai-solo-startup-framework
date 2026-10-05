# Delta for a2a-transport

## MODIFIED Requirements

### Requirement: Push Notifications Stream Task State in Real Time

Each supervisor MUST offer a push notification stream (SSE) that emits task state changes as
they occur, consumable by the monitoring UI without polling. The notification path MUST flow
via an `OnStateChange` callback registered on the supervisor at startup: the callback invokes
`UIHandler.Broadcast(sup.Status())`, which serializes the supervisor status as JSON and
delivers a `state` SSE event to all connected clients. The supervisor MUST expose a
`SetOnStateChange(fn func())` method to allow the wiring layer (`main.go`) to register the
callback after both the supervisor and the UI handler are constructed, avoiding import cycles.

(Previously: requirement stated push notifications MUST be offered but did not specify the
supervisor-side callback mechanism — the wiring was missing, leaving SSE driven only by
keep-alive pings with no state events ever emitted.)

#### Scenario: A state transition appears on the stream without polling

- GIVEN a monitoring UI client is subscribed to a supervisor's push stream
- WHEN one of that supervisor's tasks transitions from `WORKING` to `INPUT_REQUIRED`
- THEN the subscribed client receives that transition on the stream without issuing a new
  request

#### Scenario: Callback registered after both objects are constructed

- GIVEN the supervisor is created before `UIHandler` (agent materialization precedes server
  setup in `main.go`)
- WHEN `main.go` calls `sup.SetOnStateChange(func() { handler.Broadcast(sup.Status()) })`
  after both are initialized
- THEN subsequent supervisor state changes trigger the registered broadcast
