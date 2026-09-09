# Delta for a2a-transport

## MODIFIED Requirements

### Requirement: Core Task Operations Are Available Over A2A

Each supervisor MUST support `SendMessage`, `GetTask`, `ListTasks`, `CancelTask`, and
`SubscribeToTask` as A2A operations against its own task set. All operations MUST require a
valid Bearer token (enforced by authInterceptor). `ListTasks` MUST return only tasks owned by
the authenticated caller; callers with different identities MUST NOT observe each other's tasks.

(Previously: operations required no authentication; `ListTasks` returned all tasks regardless
of caller because the authenticator always returned the fixed identity `"supervisor"`.)

#### Scenario: Authenticated client lists only its own tasks

- GIVEN a supervisor has tasks owned by token-A and tasks owned by token-B
- WHEN the token-A holder calls `ListTasks` with a valid token
- THEN only token-A's tasks are returned

#### Scenario: Unauthenticated client is denied

- GIVEN a client sends `ListTasks` with no Authorization header
- WHEN the authInterceptor processes it
- THEN the server returns a JSON-RPC unauthorized error
- AND no tasks are returned
