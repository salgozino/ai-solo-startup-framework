# Delta for Provider Adapter

> **Target note**: This delta MODIFIES the requirement "Providers Declare Their Capabilities"
> in the PENDING, UNARCHIVED spec `openspec/changes/agent-startup-framework-foundation/specs/provider-adapter/spec.md`.
> `openspec/specs/provider-adapter/spec.md` does not yet exist. `sdd-archive` MUST merge this
> block into the pending spec's matching requirement when both changes archive — it MUST NOT
> create a separate `provider-adapter` capability entry.

## MODIFIED Requirements

### Requirement: Providers Declare Their Capabilities

A conforming provider MUST expose a capability declaration via a `Capabilities() ProviderCapabilities`
method on `port.Provider`, returning `ProviderCapabilities{ContextBudget int; ActionKinds []string}`,
queryable by the supervisor before or independently of any specific invocation. `ProviderCapabilities`
MUST be provider-owned: each adapter (`claudecode.Adapter`, `opencode.Adapter`) derives `ActionKinds`
from the `risk_policy` action kinds it declares as MCP tools, and returns its own `ContextBudget`.
`core/supervisor/supervisor.go`'s `Config.ContextBudget` becomes an optional operator override: when
non-zero, the supervisor uses it in place of the provider's declared value; when zero, the supervisor
uses `Provider.Capabilities().ContextBudget`. `fake.Provider` MUST implement `Capabilities()` so
tests can configure a canned declaration without a real adapter.

(Previously: specified but never implemented. `ProviderCapabilities` had no Go type anywhere in
the module — only a dangling doc comment at `core/port/provider.go:101`. `ContextBudget` existed
solely as a supervisor-owned, operator-configured field (`core/supervisor/supervisor.go:29`),
inverting this requirement's declared provider-ownership direction. No test referenced
`Capabilit*` anywhere in the module.)

#### Scenario: Supervisor reads provider capabilities before assembling context

- GIVEN a supervisor is about to invoke a provider that declares `ContextBudget: 8000`
- WHEN it calls `Provider.Capabilities()` before invocation
- THEN it receives a `ProviderCapabilities` value whose `ContextBudget` is `8000`, usable to
  cap the context it assembles

#### Scenario: An operator-configured override takes precedence

- GIVEN `Config.ContextBudget` is set to a non-zero value by the operator
- WHEN the supervisor determines the effective context budget for a task
- THEN it uses `Config.ContextBudget`, not the provider's declared value

#### Scenario: The provider-declared budget is the default absent an override

- GIVEN `Config.ContextBudget` is zero (unset)
- WHEN the supervisor determines the effective context budget for a task
- THEN it uses `Provider.Capabilities().ContextBudget`

#### Scenario: `fake.Provider` conforms to the capability contract

- GIVEN a test configures `fake.Provider`'s capability return value
- WHEN `Capabilities()` is called
- THEN it returns the configured `ProviderCapabilities`, enabling capability-driven supervisor
  tests without a real adapter
