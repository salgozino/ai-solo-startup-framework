# Telegram Gateway Specification

## Purpose

The Telegram gateway is a company-level, outbound-only capability for delivering a message to
the company owner. It has no inbound surface and no caller-chosen recipient; it is reachable
only through a policy-authorized action.

## Requirements

### Requirement: Outbound Only, No Inbound Handling

The Telegram gateway MUST only send messages outward; it MUST NOT process, route, or expose any
inbound Telegram surface (incoming messages, commands, or webhooks).

#### Scenario: No inbound message is ever processed

- GIVEN the Telegram gateway is configured and running
- WHEN an inbound message arrives at the associated Telegram bot from any source
- THEN the framework takes no action derived from that inbound message — there is no inbound
  handling path

### Requirement: Recipient Comes From Configuration, Never From the Caller

The gateway's `Send` operation MUST resolve its recipient exclusively from company configuration
(the owner's configured identifier); it MUST NOT accept or honor a caller-supplied recipient.

#### Scenario: An action intent carrying its own recipient is ignored

- GIVEN a `ceo` agent's action intent for `telegram_send` includes a recipient value chosen by
  the agent
- WHEN the gateway executes the approved action
- THEN the message is delivered to the configured owner, and the agent-supplied recipient value
  has no effect on where it is delivered

### Requirement: The Gateway Is Reachable Only Through a Policy-Authorized Action

The gateway's `Send` operation MUST be reachable only via an action that the risk policy has
authorized (permitted role, and either non-risky-permitted or human-approved-if-risky); there
MUST be no path from a denied or rejected intent to a `Send` call.

#### Scenario: A hard-denied intent never reaches Send

- GIVEN an `engineer` agent's `telegram_send` intent is hard-denied because `engineer` is not in
  `allowed_roles`
- WHEN the policy engine finishes classifying the intent
- THEN the gateway's `Send` operation is never invoked for that intent

#### Scenario: A rejected escalation never reaches Send

- GIVEN a `ceo` agent's `telegram_send` escalation is pending in `INPUT_REQUIRED`
- WHEN the human rejects it
- THEN the gateway's `Send` operation is never invoked for that intent

### Requirement: Send Failure Maps to a Failed Task, Never a Supervisor Crash

If a `Send` attempt fails (network error, API error, or similar), the failure MUST be surfaced
as the owning task transitioning to `FAILED`; it MUST NOT crash or destabilize the supervisor
process.

#### Scenario: A failed Telegram send fails the task, not the supervisor

- GIVEN an approved `telegram_send` action is executed and the underlying Telegram API call
  fails
- WHEN the gateway reports the failure
- THEN the owning task transitions to `FAILED`, and the supervisor process continues running
  normally for other tasks

### Requirement: Bot Token Is Supplied via Environment, Never Committed

The gateway MUST read its bot token from an environment variable at runtime; it MUST NOT accept
a token value written directly into `company.yaml`.

#### Scenario: A gateway with no environment token configured fails closed

- GIVEN the environment variable referenced by the gateway's `token_env` is unset
- WHEN the CLI attempts to materialize a company that declares the Telegram gateway
- THEN materialization fails or the gateway is unusable, rather than silently sending with a
  missing or empty token
