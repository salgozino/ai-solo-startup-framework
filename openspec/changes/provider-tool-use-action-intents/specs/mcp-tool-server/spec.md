# MCP Tool Server Specification

## Purpose

One long-lived MCP server, started at framework startup and alive until the binary exits,
exposes one MCP tool per `risk_policy` action kind over HTTP. It binds each adapter invocation
to a per-invocation bearer token resolved to `{tenant, agent, taskID}` (mirrors
`transport/a2a`'s bearer-auth + tenant-interceptor pattern), and acknowledges `tools/call`
without executing — recording an `ActionIntent` in an authenticated sink instead.

## Requirements

### Requirement: Server Starts Once at Framework Startup

The framework MUST start exactly one long-lived HTTP MCP server during `materialize`, before
any supervisor accepts tasks, and keep it running until the binary exits. MUST NOT be
spawned per-invocation; MUST NOT use stdio transport.

#### Scenario: Server starts before any supervisor is ready

- GIVEN the framework is materializing a company
- WHEN startup completes
- THEN exactly one MCP HTTP server is listening and remains alive for the process lifetime

#### Scenario: Server startup failure aborts materialize

- GIVEN the MCP server fails to bind its listening address
- WHEN `materialize` runs
- THEN it aborts before any supervisor is marked ready, and no adapter is invoked

### Requirement: One Tool Per Risk-Policy Action Kind

The server MUST register one MCP tool per action kind present in `risk_policy`
(`config.Policy` map keys), and MUST NOT register tools for undeclared action kinds.

#### Scenario: Tool registry mirrors configured action kinds

- GIVEN `company.yaml` declares a `risk_policy` entry for `telegram_send`
- WHEN the MCP server starts
- THEN it exposes exactly one MCP tool for `telegram_send` and no tool for any undeclared kind

### Requirement: Per-Invocation Token Binds Adapter to Tenant, Agent, and Task

Before an adapter spawns a subprocess for an agent role bound to a tenant, the server-owned
invocation registry MUST mint a single-use bearer token bound to `{tenant, agent, taskID}`.
The adapter MUST pass this token as an HTTP header on every MCP request for that invocation
(mirrors `transport/a2a`'s `bearerPrefix` convention). The registry MUST release the token on
process exit.

#### Scenario: Token resolves to the invoking tenant, agent, and task

- GIVEN an adapter mints a token for agent `worker` under tenant `acme`, task `t1`
- WHEN the MCP server receives a `tools/call` request bearing that token
- THEN it resolves the request to `{tenant: acme, agent: worker, taskID: t1}` before invoking
  any handler

#### Scenario: A token resolving to a different tenant is rejected

- GIVEN a token was minted for tenant `acme`
- WHEN an MCP request presents that token in a context routed for tenant `beta`
- THEN the server rejects the request before invoking any tool handler, and no `ActionIntent`
  is recorded

#### Scenario: Unknown or expired token is rejected

- GIVEN a token that was never minted, or was already released
- WHEN an MCP request presents it
- THEN the server rejects the request and records no intent

### Requirement: `tools/call` Acknowledges Without Executing

The `tools/call` handler MUST record an `ActionIntent{Kind, Payload}` in the sink for the
resolved `{tenant, agent, taskID}` and return a synchronous acknowledgment to the caller. It
MUST NOT invoke `Gateway.Send` or trigger any external effect. Each tool's description MUST
state that invoking it only records intent and does not perform the action.

#### Scenario: A valid tool call is acknowledged without side effects

- GIVEN a valid token and a registered tool `telegram_send`
- WHEN the model invokes `tools/call` for that tool with a message body
- THEN the server records `ActionIntent{Kind: "telegram_send", Payload: {...}}` in the sink and
  returns success synchronously, and no gateway send occurs as a direct result of this call

#### Scenario: Tool description discloses the acknowledge-without-execute contract

- GIVEN the MCP server's tool registry
- WHEN a client lists available tools
- THEN each tool's description states that calling it records an intent for later policy
  classification and does not execute the action

### Requirement: The Intent Sink Is the Sole Source of `ActionIntents`

`ActionIntent`s consumed by `Supervisor.executeWithPolicy` MUST originate only from the
sink populated by `tools/call`, keyed by the invocation's token. No component MAY construct
`ActionIntent`s by parsing subprocess output.

#### Scenario: Sink is authoritative regardless of CLI text output

- GIVEN a completed adapter invocation whose sink recorded one intent
- WHEN the adapter builds its `ProviderResult`
- THEN `ActionIntents` reflects exactly the sink's recorded intents for that invocation's
  token, independent of what the CLI printed to stdout

**Agent roles**: all roles with `risk_policy` entries. **Protocol**: MCP over HTTP (JSON-RPC
`tools/call`), token via `Authorization: Bearer <token>` header — mirrors `transport/a2a`.
