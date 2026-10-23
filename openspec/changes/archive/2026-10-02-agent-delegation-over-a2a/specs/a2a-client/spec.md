# A2A Client Specification

## Purpose

Outbound A2A communication used by delegation: the framework resolves a peer agent's card,
authenticates with the shared bearer token, propagates the correct tenant, and blocks until the
peer's task reaches a terminal state. This is the client-side counterpart to
`transport/a2a/server.go`'s existing bearer-auth (`authInterceptor`) and tenant-interceptor
(`tenantInterceptor`) enforcement — no server-side behavior changes.

## Requirements

### Requirement: The Client Resolves the Peer's Agent Card Before Sending

Before sending a message to a peer, the A2A client MUST resolve the peer's Agent Card from the
peer's base URL (as recorded in the peer directory) using the A2A client library's card
resolution mechanism. The client MUST NOT construct or send a request against a raw URL without
having resolved a card for it first.

#### Scenario: A valid peer base URL resolves an Agent Card before any message is sent

- GIVEN the peer directory maps role `engineer` to a live base URL
- WHEN the A2A client delegates to `engineer`
- THEN it first resolves that peer's Agent Card from the base URL, and only then issues the
  outbound message

#### Scenario: A peer whose card cannot be resolved fails the delegation, not a raw send

- GIVEN a peer's base URL is registered in the peer directory but its Agent Card endpoint is
  unreachable
- WHEN the A2A client attempts to delegate to that peer
- THEN card resolution fails and the delegation fails with that error — no raw, unresolved
  request is attempted against the peer

### Requirement: The Client Authenticates Every Outbound Request With the Shared Bearer Token

Every outbound request the A2A client makes to a peer MUST carry the shared bearer token as an
`Authorization: Bearer <token>` header, supplied through the A2A client library's authentication
interceptor mechanism. A request sent without this header MUST NOT occur.

#### Scenario: An outbound delegation request carries the shared bearer token

- GIVEN the framework's shared A2A bearer token is configured
- WHEN the A2A client sends a delegation message to a peer
- THEN the peer's `authInterceptor` observes a valid `Authorization: Bearer <token>` header on
  the request, matching the shared token

#### Scenario: A request without a valid token is rejected by the peer, proving the client cannot bypass auth

- GIVEN a peer supervisor requires a valid bearer token
- WHEN a request is sent with a missing or incorrect token
- THEN the peer's `authInterceptor` rejects it with an unauthenticated error, and no task is
  created on the peer

### Requirement: The Client Sets the Request Tenant So the Peer's Tenant Check Accepts It

Every outbound `SendMessage` request the A2A client makes MUST set its tenant to the delegating
company's own tenant identifier. Because the peer supervisor is bound to the same company's
tenant, this MUST cause the peer's `tenantInterceptor` to accept the request.

#### Scenario: A delegation request's tenant matches the peer's bound tenant

- GIVEN both the delegating and peer supervisors belong to tenant `acme`
- WHEN the A2A client sends a delegation `SendMessage` request
- THEN the request's tenant field is `acme`, and the peer's `tenantInterceptor` accepts it

### Requirement: `SendMessage` Blocks Until the Peer Task Is Terminal, Bounded by a Deadline

The A2A client's outbound `SendMessage` operation MUST block the caller until the peer's task
reaches a terminal A2A `TaskState`, subject to a caller-supplied context deadline. When the
context deadline is reached before the peer's task is terminal, the call MUST return an error
distinguishable as a timeout, without silently returning a partial or fabricated result.

#### Scenario: A completed peer task unblocks the caller with its terminal output

- GIVEN a delegation request has been sent to a peer whose task later reaches `COMPLETED`
- WHEN the peer's task reaches `COMPLETED`
- THEN the A2A client's blocking call returns with that task's terminal output and no error

#### Scenario: An expired deadline returns a distinguishable timeout error

- GIVEN a delegation request's context has a deadline shorter than the peer's task takes to
  reach a terminal state
- WHEN the deadline is reached
- THEN the A2A client's blocking call returns an error distinguishable as a deadline/timeout,
  and does not return a fabricated success result

**Agent roles**: consumed on behalf of any role permitted to delegate (per `risk_policy`);
addresses peer-side roles resolved through the peer directory. **Protocol**: A2A JSON-RPC over
HTTP, Bearer auth via `AuthInterceptor`, tenant-scoped via `SendMessageRequest.Tenant` —
client-side counterpart of the existing `transport/a2a` server enforcement.
