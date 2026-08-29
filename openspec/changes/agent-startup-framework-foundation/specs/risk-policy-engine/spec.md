# Risk Policy Engine Specification

## Purpose

Every action intent emitted by a provider is classified before it can produce any external
effect. Classification is two-stage — capability, then risk — in that fixed order, and a
capability denial is never softened into an approval opportunity.

## Requirements

### Requirement: Capability Check Precedes Risk Check

The policy engine MUST evaluate whether the emitting agent's role is in the action kind's
`allowed_roles` before evaluating the action's risk level; risk is never evaluated for a role
that is not allowed to perform the action kind at all.

#### Scenario: Capability check runs first for every intent

- GIVEN an action intent of kind `telegram_send` from an agent with role `engineer`, where
  `engineer` is not in `telegram_send`'s `allowed_roles`
- WHEN the policy engine classifies the intent
- THEN the capability check fails before any risk-level evaluation occurs

### Requirement: A Disallowed Role Is Hard-Denied Without Escalation

When an agent's role is not in an action kind's `allowed_roles`, the policy engine MUST deny the
intent outright, drive the owning task to `REJECTED`, and MUST NOT offer the action for human
escalation under any circumstance.

#### Scenario: Disallowed role produces a rejection with no escalation

- GIVEN a `worker` agent with role `engineer` emits a `telegram_send` intent, and `engineer` is
  not in `allowed_roles` for `telegram_send`
- WHEN the policy engine classifies the intent
- THEN the task is driven to `REJECTED`, zero external effect occurs, and no escalation is ever
  surfaced to a human

### Requirement: An Allowed, Risky Action Escalates to the Human

When an agent's role is allowed to perform an action kind and that action kind is classified
`risky`, the policy engine MUST escalate the task (transition to `INPUT_REQUIRED`) rather than
executing the action directly.

#### Scenario: CEO's risky, permitted send escalates

- GIVEN a `ceo` agent emits a `telegram_send` intent, `ceo` is in `allowed_roles` for
  `telegram_send`, and `telegram_send` is classified `risky`
- WHEN the policy engine classifies the intent
- THEN the task transitions to `INPUT_REQUIRED` and no external effect occurs until a human
  verdict is recorded

### Requirement: An Allowed, Non-Risky Action Executes Directly

When an agent's role is allowed to perform an action kind and that action kind is not classified
`risky`, the policy engine MUST permit execution without escalation.

#### Scenario: A non-risky permitted action does not wait for a human

- GIVEN an agent's role is allowed for an action kind classified as not `risky`
- WHEN the policy engine classifies a matching intent
- THEN the action is authorized for execution without entering `INPUT_REQUIRED`

### Requirement: Denial and Rejection Are Provably Unreachable, Not Merely Blocked

A denied or rejected intent MUST result in zero external effect and MUST leave no code path by
which the effect could still occur; a denial MUST be observably distinguishable from "approved
but the effect failed for an unrelated reason."

#### Scenario: A hard-denied intent leaves no trace of an attempted send

- GIVEN an intent is hard-denied by the capability check
- WHEN the task's final state and any gateway activity are inspected
- THEN the gateway records no attempted send for that intent, and the task's terminal state is
  `REJECTED`, not `FAILED`

### Requirement: Policy Rules Are Declared Once, Per Action Kind

Each action kind's risk level and `allowed_roles` MUST be declared exactly once, in the
company's policy configuration, as the single authorization source for that action kind.

#### Scenario: A single declaration governs all agents for an action kind

- GIVEN `telegram_send` is declared once with `allowed_roles: [ceo]`
- WHEN any agent of any role emits a `telegram_send` intent
- THEN that single declaration is the complete authorization source — no other configuration
  overrides or supplements it
