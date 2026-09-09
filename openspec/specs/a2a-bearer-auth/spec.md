# a2a-bearer-auth Specification

## Purpose

Authenticate every inbound A2A request via a shared Bearer token. Caller identity
established by the interceptor is consumed by task ownership and tenant scoping logic.

## Requirements

### Requirement: AuthToken Configuration Is Required

`CompanyConfig.AuthToken` MUST be a non-empty string. `New()` MUST fail with a descriptive
error and refuse to start when the token is absent or empty.

#### Scenario: Server starts with a configured token

- GIVEN `CompanyConfig.AuthToken` is a non-empty value
- WHEN `a2a.New()` is called
- THEN the server initializes successfully and accepts requests

#### Scenario: Server refuses to start without a token

- GIVEN `CompanyConfig.AuthToken` is empty
- WHEN `a2a.New()` is called
- THEN it returns an error describing that `auth_token` is not configured
- AND the server does not start

### Requirement: Every Request Must Present a Valid Bearer Token

Every inbound A2A request MUST carry `Authorization: Bearer <token>`. The authInterceptor
MUST reject any request with a missing, malformed, or non-matching token with a JSON-RPC
unauthorized error before any handler runs. On success it MUST set `callCtx.User.Name` to
identify the caller.

#### Scenario: Valid token is accepted

- GIVEN a client sends `Authorization: Bearer <configured-token>`
- WHEN the authInterceptor processes it
- THEN the request proceeds and `callCtx.User.Name` is set

#### Scenario: Missing Authorization header is rejected

- GIVEN a client sends a request with no Authorization header
- WHEN the authInterceptor processes it
- THEN the server returns a JSON-RPC unauthorized error
- AND no task is created or modified

#### Scenario: Incorrect token is rejected

- GIVEN a client sends `Authorization: Bearer wrong-token`
- WHEN the authInterceptor processes it
- THEN the server returns a JSON-RPC unauthorized error

### Requirement: authInterceptor Executes Before tenantInterceptor

The authInterceptor MUST be the first interceptor in the chain. `callCtx.User.Name` MUST be
set before tenantInterceptor validates the tenant.

#### Scenario: Auth precedes tenant validation

- GIVEN a request with a valid Bearer token and a valid tenant
- WHEN the interceptor chain executes
- THEN `callCtx.User.Name` is set before tenantInterceptor processes the request

### Requirement: Agent Card Declares httpBearer Security Scheme

Each supervisor's Agent Card MUST declare `httpBearer` as its security scheme so clients
know authentication is required before making requests.

#### Scenario: Card includes security annotation

- GIVEN a supervisor is running with Bearer auth configured
- WHEN a client fetches the Agent Card
- THEN the card includes an `httpBearer` security scheme declaration
