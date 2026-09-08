# SSE Graceful Shutdown Specification

## Purpose

Defines behavior for explicitly shutting down the UIHandler's SSE layer so that
active SSE streams close before the HTTP server drains. Applies to the
Monitoring UI component and the UIHandler protocol.

## Requirements

### Requirement: SSE Stream Shutdown Signal

The UIHandler MUST expose an explicit shutdown mechanism that closes all active
SSE streams immediately when invoked. The mechanism MUST be idempotent — calling
it more than once MUST NOT cause a panic or error. The SSE event loop MUST exit
within 100 ms of the signal being issued.

#### Scenario: Active SSE client receives shutdown signal

- GIVEN an SSE client is connected and the UIHandler is streaming events
- WHEN the UIHandler shutdown mechanism is invoked
- THEN the SSE event loop exits and the response is finalized
- AND the exit occurs within 100 ms of the signal

#### Scenario: No active SSE clients at shutdown time

- GIVEN no SSE clients are connected to the UIHandler
- WHEN the UIHandler shutdown mechanism is invoked
- THEN the shutdown completes without error or observable side effect

#### Scenario: Shutdown invoked more than once (idempotency)

- GIVEN the UIHandler shutdown mechanism was already invoked once
- WHEN the shutdown mechanism is invoked a second time
- THEN no panic or error occurs
- AND the system state remains valid

### Requirement: Graceful HTTP Server Drain

The HTTP server's graceful shutdown sequence MUST close all SSE streams before
calling the HTTP server drain. The server MUST reach a fully drained state within
1 second when SSE streams have been closed first.

#### Scenario: Server shutdown with active SSE client

- GIVEN an SSE client is connected and streaming
- WHEN the application shutdown sequence is triggered
- THEN SSE streams are closed before the HTTP server drain begins
- AND the HTTP server reaches a fully drained state within 1 second

#### Scenario: Server shutdown with no connected clients

- GIVEN no SSE clients are connected
- WHEN the application shutdown sequence is triggered
- THEN the HTTP server drain completes immediately without waiting

### Requirement: Post-Shutdown Broadcast Safety

The UIHandler MUST remain safe to call broadcast-style operations on after
shutdown has been invoked. Any event published after shutdown MUST be silently
dropped without causing a panic or blocking the caller.

#### Scenario: Broadcast called after shutdown

- GIVEN the UIHandler shutdown mechanism has been invoked
- WHEN an event is broadcast to the UIHandler
- THEN the event is silently dropped
- AND the caller is not blocked or panicked
