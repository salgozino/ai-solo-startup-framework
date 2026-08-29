# A2A Transport Specification

## Purpose

Every supervisor communicates over a real A2A wire on loopback — no in-process shortcuts. This
spec covers Agent Card publication, task operations, tenant handling, and push notifications as
observed by any A2A client, including another supervisor or the monitoring UI.

## Requirements

### Requirement: Every Supervisor Publishes a Discoverable Agent Card

Each supervisor, regardless of agent role, MUST publish its own Agent Card at a discoverable
endpoint on startup, before it can receive tasks.

#### Scenario: A newly started supervisor is discoverable

- GIVEN a supervisor for the `ceo` role has just started
- WHEN another supervisor or client requests its Agent Card
- THEN it receives a valid Agent Card describing that supervisor's endpoint and capabilities

### Requirement: Inter-Agent Communication Uses Real A2A Transport, Not In-Process Calls

Communication between two supervisors (for example, `ceo` delegating to `engineer`) MUST occur
over the real A2A wire on loopback; no component may bypass the wire with an in-process function
call standing in for a network request.

#### Scenario: CEO delegates to worker over the real wire

- GIVEN a `ceo` supervisor and an `engineer` supervisor are both running on loopback
- WHEN the `ceo` supervisor sends a task to the `engineer` supervisor
- THEN the request traverses the A2A transport (serialization, endpoint, real request/response),
  observable independently of either supervisor's in-process code path

### Requirement: Core Task Operations Are Available Over A2A

Each supervisor MUST support `SendMessage`, `GetTask`, `ListTasks`, `CancelTask`, and
`SubscribeToTask` as A2A operations against its own task set.

#### Scenario: A client lists tasks for a supervisor

- GIVEN a supervisor has one task in `WORKING` and one in `COMPLETED`
- WHEN a client calls `ListTasks` against that supervisor
- THEN both tasks are returned with their current states

### Requirement: Every Request Carries and Validates a Tenant

Every A2A request MUST carry a `tenant` value, and a supervisor MUST reject any request whose
tenant is present-but-empty at the transport edge, because an empty and an absent tenant are
otherwise indistinguishable.

#### Scenario: A request with a non-empty tenant is accepted

- GIVEN a client sends a `SendMessage` request with `tenant: acme`
- WHEN the supervisor receives it
- THEN the request is accepted and routed under the `acme` tenant

#### Scenario: A request with an empty tenant is rejected at the edge

- GIVEN a client sends a request with `tenant: ""`
- WHEN the supervisor receives it
- THEN the request is rejected before it reaches task processing, and no task is created or
  modified under any tenant

### Requirement: Push Notifications Stream Task State in Real Time

Each supervisor MUST offer a push notification stream (SSE) that emits task state changes as
they occur, consumable by the monitoring UI without polling.

#### Scenario: A state transition appears on the stream without polling

- GIVEN a monitoring UI client is subscribed to a supervisor's push stream
- WHEN one of that supervisor's tasks transitions from `WORKING` to `INPUT_REQUIRED`
- THEN the subscribed client receives that transition on the stream without issuing a new
  request
