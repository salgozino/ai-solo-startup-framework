# Delta for Company-as-Code

> **Target note**: This delta targets the PENDING, UNARCHIVED spec
> `openspec/changes/agent-startup-framework-foundation/specs/company-as-code/spec.md`.
> `openspec/specs/company-as-code/spec.md` does not yet exist. `sdd-archive` MUST append the
> `ADDED Requirements` below to that pending spec when both changes archive.

## ADDED Requirements

### Requirement: Agent Roles Are Unique Within a Company

`company.yaml` MAY declare multiple agents sharing the same `provider`, but no two agents in
the same company MAY declare the same `role`. Materializing a company whose agents declare a
duplicate role MUST fail with an explicit configuration error, surfaced before any supervisor
in that company is marked ready — never a silent last-one-wins registration and never a
nil-pointer panic downstream in role-addressed lookup (for example, the peer directory).

#### Scenario: Two agents declaring the same role fail materialize with a named error

- GIVEN a `company.yaml` declares two agents that both set `role: engineer`
- WHEN the CLI's materialize command runs
- THEN it fails with an explicit error naming the duplicate role `engineer`, and no supervisor
  in that company is marked ready

#### Scenario: Distinct roles across agents materialize normally

- GIVEN a `company.yaml` declares a `ceo` agent and an `engineer` agent with distinct roles
- WHEN the CLI's materialize command runs
- THEN materialization proceeds normally, with each role resolvable to exactly one agent

### Requirement: Delegation Is Declared Like Any Other Risk-Policy Action Kind

The `delegate_task` action kind MUST be declarable in `company.yaml`'s `risk_policy` with the
same shape as any other action kind (`risk` and `allowed_roles`), governed by the same
declare-once-per-kind rule as every other action kind (see `risk-policy-engine`: "Policy Rules
Are Declared Once, Per Action Kind"). The shipped `company.yaml` MUST declare `delegate_task`
with `allowed_roles: [ceo]`.

#### Scenario: The shipped company.yaml declares delegation restricted to the CEO role

- GIVEN the framework's shipped `company.yaml`
- WHEN its `risk_policy` is inspected
- THEN it declares a `delegate_task` entry whose `allowed_roles` is exactly `[ceo]`

#### Scenario: Removing the `delegate_task` policy entry disables delegation entirely

- GIVEN `company.yaml`'s `risk_policy` has its `delegate_task` entry removed
- WHEN the MCP server starts and the policy engine classifies any `delegate_task` intent
- THEN no `delegate_task` MCP tool is generated, and any such intent (were one recorded) would
  be hard-denied as an undeclared action kind — delegation is fully disabled by config alone
