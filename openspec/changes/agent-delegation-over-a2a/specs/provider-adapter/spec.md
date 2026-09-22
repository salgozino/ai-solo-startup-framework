# Delta for Provider Adapter (A2A Network Methods Removed)

> **Target note**: This delta targets the PENDING, UNARCHIVED spec
> `openspec/changes/agent-startup-framework-foundation/specs/provider-adapter/spec.md`.
> `openspec/specs/provider-adapter/spec.md` does not yet exist. A separate pending delta from
> `provider-tool-use-action-intents` also targets this same spec (modifying "Providers Declare
> Their Capabilities"); the two deltas touch different requirements and MUST both be merged into
> the same pending spec when all three changes archive — neither delta creates a competing
> `provider-adapter` capability entry.

## ADDED Requirements

### Requirement: `port.Provider` Is Scoped to Local Execution and Lifecycle Reporting, Not A2A Networking

`port.Provider` MUST NOT declare any method for sending a message to, streaming from, or
resolving the address of a peer agent over A2A. A conforming provider's contract is limited to:
completing/failing a task it owns (`Complete`, `CompleteError`), dispatching a capability task
(`SendTask`), executing a task locally (`RunTask`), and declaring its capabilities
(`Capabilities`). Outbound A2A networking (addressing a peer, sending to it, and blocking for
its terminal result) is exclusively the responsibility of the dedicated delegation port
(`agent-delegation` capability) and the A2A client (`a2a-client` capability); `port.Provider`
MUST NOT be extended to reintroduce it.

#### Scenario: `port.Provider`'s method set excludes A2A network operations

- GIVEN the `port.Provider` interface definition
- WHEN its method set is inspected
- THEN it declares exactly `Complete`, `CompleteError`, `SendTask`, `RunTask`, and
  `Capabilities` — no `SendMessage`, `SendMessageStream`, or `ResolveAgent` method is present

#### Scenario: A conforming adapter implements no A2A-networking stub

- GIVEN an adapter (for example the Claude Code or OpenCode adapter) satisfies `port.Provider`
- WHEN its source is inspected
- THEN it contains no `SendMessage`, `SendMessageStream`, or `ResolveAgent` method, and in
  particular no method that unconditionally returns a "not implemented" error for these
  operations

#### Scenario: `fake.Provider` mirrors the narrowed contract

- GIVEN a test constructs `fake.Provider` to satisfy `port.Provider`
- WHEN its method set is inspected
- THEN it implements exactly the narrowed method set above, with no A2A-networking methods or
  fields left over from the previous, wider contract
