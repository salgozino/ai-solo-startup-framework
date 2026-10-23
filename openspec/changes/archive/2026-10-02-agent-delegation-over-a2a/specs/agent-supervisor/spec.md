# Delta for Agent Supervisor

> **Target note**: This delta targets the PENDING, UNARCHIVED spec
> `openspec/changes/agent-startup-framework-foundation/specs/agent-supervisor/spec.md`.
> `openspec/specs/agent-supervisor/spec.md` does not yet exist. `sdd-archive` MUST append the
> `ADDED Requirements` below to that pending spec when both changes archive.
>
> This delta does NOT touch "Crash and Restart Recovery". The awaiting-peer recovery
> modification of that requirement was MOVED to
> `openspec/changes/agent-delegation-chained-approval/specs/agent-supervisor/spec.md` when this
> change was scoped down to design slices 1–6 and 10.

## ADDED Requirements

### Requirement: Supervisor Construction Requires a Non-Nil Policy Engine

Constructing a `Supervisor` with a nil `Config.PolicyEngine` MUST return an explicit
construction error and MUST NOT produce a `Supervisor` that later nil-pointer-panics when a
task is executed. A `PolicyEngine` MUST be required for every supervisor; there is no supported
"delegation-only, no policy" construction mode.

#### Scenario: Constructing a supervisor without a policy engine fails at construction, not at task time

- GIVEN `supervisor.New` is called with `Config.PolicyEngine` left nil
- WHEN construction is attempted
- THEN it returns an explicit error before any task can be accepted, and no `Supervisor`
  capable of accepting tasks is produced

#### Scenario: A configured policy engine constructs successfully

- GIVEN `supervisor.New` is called with a non-nil `Config.PolicyEngine`
- WHEN construction is attempted
- THEN it succeeds and the resulting supervisor is ready to progress to `IDLE`

### Requirement: A Permitted Delegation Intent Routes to the Delegation Port, Not the Gateway

When `executeAction` processes an action intent classified `Permit` whose kind is
`delegate_task`, it MUST route execution to the configured delegation port's blocking delegate
operation, passing the intent's target role and body, rather than to `Gateway.Send`. The peer's
terminal output returned by the delegation port MUST be written into the task's output before
the task is marked `COMPLETED`.

#### Scenario: A Permitted `delegate_task` intent calls the delegation port, not the gateway

- GIVEN a `delegate_task` intent classified `Permit` for a task
- WHEN `executeAction` processes it
- THEN it calls the configured delegation port's delegate operation with the intent's target
  role and body, and `Gateway.Send` is never called for this intent

#### Scenario: Any other Permitted action kind still routes to the gateway as before

- GIVEN a `telegram_send` intent classified `Permit`
- WHEN `executeAction` processes it
- THEN it calls `Gateway.Send` exactly as before this change, unaffected by the new delegation
  routing
