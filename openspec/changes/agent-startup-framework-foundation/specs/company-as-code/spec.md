# Company-as-Code Specification

## Purpose

A human declares the company as a single `company.yaml` file — tenant, agents, gateways, and
risk policy. The CLI materializes this declaration into running supervisors wired for A2A. This
spec covers the declarative contract and materialization behavior, not the runtime behavior of
the materialized agents themselves.

## Requirements

### Requirement: Company Definition Declares Tenant, Agents, Gateways, and Policy

A `company.yaml` MUST declare a tenant identifier, one or more agents (each with a name, role,
and provider), the company's outbound gateways, and the risk policy governing action kinds.

#### Scenario: A minimal valid company file is accepted

- GIVEN a `company.yaml` declares a tenant, a `ceo` agent, an `engineer` agent, a `telegram`
  gateway, and a `telegram_send` policy entry
- WHEN the CLI validates the file
- THEN it is accepted as a complete company definition

### Requirement: The CLI Materializes Agents and A2A Topology

Running the CLI's materialize command against a valid `company.yaml` MUST result in one
supervisor process per declared agent, each with its own registered A2A endpoint and Agent Card,
wired for peer-to-peer communication.

#### Scenario: Materializing a two-agent company starts two supervisors

- GIVEN a `company.yaml` declares a `ceo` and an `engineer` agent
- WHEN the CLI materialize command runs
- THEN two supervisor processes start, each discoverable via its own Agent Card

### Requirement: Secrets Are Referenced by Environment Variable, Never Inlined

Any credential referenced by `company.yaml` (for example, a gateway bot token) MUST be declared
as an environment variable reference; the CLI MUST reject a `company.yaml` that contains a
credential value inline.

#### Scenario: An inline token is rejected at load time

- GIVEN a `company.yaml` contains a literal Telegram bot token value instead of an environment
  variable reference
- WHEN the CLI loads the file
- THEN loading fails with a validation error, and no supervisor is materialized from that file

### Requirement: Gateways Are Declared Once at the Company Level

A gateway (for example, `telegram`) MUST be declared exactly once under the company's `gateways`
section; an agent entry MUST NOT carry its own gateway list, because authorization for gateway
use is governed solely by the risk policy's `allowed_roles`.

#### Scenario: An agent-level gateway field is rejected or ignored

- GIVEN a `company.yaml` includes a gateway reference on an individual agent entry rather than
  at the company level
- WHEN the CLI validates the file
- THEN the file is rejected as invalid, or the agent-level entry has no authorizing effect —
  gateway reachability is governed only by the company-level `risk_policy`

### Requirement: A Second Tenant Materializes Without Interference

Materializing a second `company.yaml` with a different tenant identifier MUST succeed without
any collision with the first tenant's agents, tasks, or state.

#### Scenario: Two tenants coexist after materialization

- GIVEN tenant `acme` has already been materialized with a `ceo` and `engineer` agent
- WHEN a second `company.yaml` for tenant `beta` with its own `ceo` and `engineer` agents is
  materialized
- THEN both tenants' supervisors run independently, and no task or state from one tenant is
  visible or reachable through the other's agents
