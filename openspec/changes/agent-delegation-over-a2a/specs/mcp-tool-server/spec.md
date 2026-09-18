# Delta for MCP Tool Server

> **Target note**: This delta MODIFIES the requirement "One Tool Per Risk-Policy Action Kind"
> in the PENDING, UNARCHIVED spec
> `openspec/changes/provider-tool-use-action-intents/specs/mcp-tool-server/spec.md`.
> `openspec/specs/mcp-tool-server/spec.md` does not yet exist. `sdd-archive` MUST merge this
> block into the pending spec's matching requirement when both changes archive — it MUST NOT
> create a separate `mcp-tool-server` capability entry.

## MODIFIED Requirements

### Requirement: One Tool Per Risk-Policy Action Kind

The server MUST register one MCP tool per action kind present in `risk_policy`
(`config.Policy` map keys), and MUST NOT register tools for undeclared action kinds. Every
tool's input schema MUST require a `body` string argument. For the `delegate_task` action kind
specifically, the input schema MUST ALSO require a `target` string argument identifying the
delegation's target role; `body` and `target` are both required for that kind. No other
currently-declared action kind requires `target`. The handler behavior described in "`tools/call`
Acknowledges Without Executing" is unchanged: recording the additional `target` argument is
still only recording intent, never a validation step that contacts a peer or executes anything.

(Previously: every tool's input schema required only `body`. `delegate_task` did not exist as
an action kind, so no kind required a second argument.)

#### Scenario: Tool registry mirrors configured action kinds

- GIVEN `company.yaml` declares a `risk_policy` entry for `telegram_send`
- WHEN the MCP server starts
- THEN it exposes exactly one MCP tool for `telegram_send` and no tool for any undeclared kind

#### Scenario: A non-delegation tool's schema requires only `body`

- GIVEN `company.yaml` declares `telegram_send` in `risk_policy`
- WHEN the MCP server generates that tool's input schema
- THEN the schema's required arguments are exactly `["body"]`

#### Scenario: The delegation tool's schema requires both `target` and `body`

- GIVEN `company.yaml` declares `delegate_task` in `risk_policy`
- WHEN the MCP server generates that tool's input schema
- THEN the schema's required arguments include both `target` and `body`, and the `target`
  argument's description states it identifies the delegation's target role

#### Scenario: Recording a delegation intent still does not execute or contact a peer

- GIVEN a valid token and the registered `delegate_task` tool
- WHEN the model invokes `tools/call` for that tool with a `target` and a `body`
- THEN the server records `ActionIntent{Kind: "delegate_task", Payload: {target, body}}` in the
  sink and returns success synchronously, and no peer is contacted as a direct result of this
  call
