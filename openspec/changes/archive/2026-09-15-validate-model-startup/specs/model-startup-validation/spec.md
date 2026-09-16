# model-startup-validation Specification

## Purpose

Ensures the framework validates model access before binding HTTP servers. An invalid or inaccessible model causes a fast-fail with a clear error; a valid model allows normal startup.

Agent roles affected: all (CEO, CTO, engineer, designer) — all roles are materialized via `materializeAgents`, which executes the startup probe before any server accepts requests.

## Requirements

### Requirement: ModelProber Interface in Adapter Package

Each adapter that wraps a model CLI MUST expose a `ProbeModel(ctx context.Context) error` operation. This capability MUST be declared within the adapter package and MUST NOT be added to the port/provider interface layer, preserving the hexagonal architecture boundary.

Agent role protocol: applies to adapter-layer implementations only; port.Provider is unchanged.

#### Scenario: Adapter exposes ProbeModel

- GIVEN an adapter instance constructed for any agent role
- WHEN the startup code type-asserts the adapter for the ModelProber capability
- THEN the assertion MUST succeed and `ProbeModel` MUST be callable

### Requirement: Startup Probe Executes Before HTTP Servers Bind

The framework MUST invoke `ProbeModel` for each adapter during startup initialization. Each probe MUST run with a maximum 15-second deadline. Startup MUST abort on probe failure; startup MUST continue normally on probe success.

Agent role protocol: `materializeAgents` in wire.go is the call site; all agent roles (CEO, CTO, engineer, designer) are probed before any server binds.

#### Scenario: Valid model — probe succeeds

- GIVEN the framework starting with a correctly configured model name
- WHEN `materializeAgents` calls `ProbeModel` on each adapter
- THEN all probes return nil and HTTP servers bind normally

#### Scenario: Invalid model — probe fails

- GIVEN the framework starting with an invalid or inaccessible model name
- WHEN `materializeAgents` calls `ProbeModel` on an adapter
- THEN the probe returns a non-nil error containing subprocess stderr
- AND the process exits before any HTTP server binds

#### Scenario: Probe exceeds 15-second deadline

- GIVEN a probe that does not respond within 15 seconds
- WHEN the context deadline elapses
- THEN `ProbeModel` returns a deadline-exceeded error and startup aborts

### Requirement: Test Isolation via Provider Override

When `wireOptions.providerOverride` is set, adapter construction and `ProbeModel` MUST be skipped entirely. Existing tests MUST remain unaffected by the startup probe.

Agent role protocol: applies to all agent roles in test contexts; override bypasses real adapter construction for all roles.

#### Scenario: Provider override present at startup

- GIVEN a startup sequence where `wireOptions.providerOverride` is non-nil
- WHEN `materializeAgents` runs
- THEN no real adapter is constructed and `ProbeModel` is never called
