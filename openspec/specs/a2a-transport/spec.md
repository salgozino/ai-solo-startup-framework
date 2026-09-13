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

### Requirement: Tenant Is a Security Boundary, Not Just a Storage Key

Every A2A request MUST carry a non-empty `tenant` value; a supervisor MUST reject any request
whose tenant is present-but-empty at the transport edge, because an empty and an absent tenant
are otherwise indistinguishable. The claimed tenant MUST also exactly match the tenant this
specific supervisor instance is bound to (`sup.Addr().Tenant()`); any other non-empty tenant MUST
be rejected as a forgery attempt, not routed elsewhere (satisfies issue #9: "tenant must be a
security boundary, not just a storage key").

(Previously: any non-empty tenant was accepted and routed under that value, with no check against
the tenant the supervisor was actually bound to.)

#### Scenario: A request matching the server's own tenant is accepted

- GIVEN a supervisor is bound to tenant `acme`
- WHEN a client sends a `SendMessage` request with `tenant: acme`
- THEN the request is accepted and routed under the `acme` tenant

#### Scenario: A request with an empty tenant is rejected

- GIVEN a client sends a request with `tenant: ""`
- WHEN the supervisor receives it
- THEN the request is rejected before it reaches task processing, and no task is created or
  modified under any tenant

#### Scenario: A request claiming a different tenant than the server's own is rejected

- GIVEN a supervisor is bound to tenant `acme`
- WHEN a client sends a `SendMessage` request with `tenant: evil-corp`
- THEN the request is rejected before it reaches task processing, and no task is created or
  modified under the `evil-corp` tenant
