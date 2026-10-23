# Delta for Monitoring UI (Client Follow-Up)

## ADDED Requirements

### Requirement: The Client Can Request Changes on a Terminal CEO Task

The UI MUST expose `POST /api/follow-up` with body `{"task_id": "<id>", "message": "<text>"}`.
`task_id` is the key the UI lists for the task (`verdict_id`, or `task_id` when absent). The
endpoint MUST answer:

- `202 Accepted` with `{"ok": true}` when the follow-up was submitted to the CEO;
- `400 Bad Request` when the body is invalid, `task_id` is empty or the message is blank;
- `404 Not Found` when no CEO task has that key (delegated tasks included);
- `409 Conflict` when the task is not in a terminal state.

A refused request MUST NOT create any task. Every terminal CEO task card MUST offer a
"Request changes" control with a message box, and a follow-up MUST be rendered nested under the
task it follows.

#### Scenario: Requesting changes on a completed CEO task

- GIVEN the CEO task T1 is `COMPLETED`
- WHEN the client posts `{"task_id": "T1", "message": "I don't like it, change X"}`
- THEN the response is `202` and a new CEO task with `follow_up_of = T1` appears nested under T1

#### Scenario: Requesting changes on a running task is refused

- GIVEN the CEO task T1 is `WORKING`
- WHEN the client posts a follow-up of T1
- THEN the response is `409` and no task is created
