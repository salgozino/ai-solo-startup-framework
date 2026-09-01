# Provider Adapter Specification

## Purpose

The provider adapter contract is the seam between the framework core and any AI provider. It
MUST be specifiable, testable, and satisfiable without reference to a particular provider
implementation; any conforming provider — including but not limited to Claude Code — plugs into
the same supervisor lifecycle without the framework core knowing which provider it is.

## Requirements

### Requirement: Task Invocation Takes a Full Address and Bounded Context

A conforming provider MUST accept a task invocation carrying the agent's full A2A address
(`agent-name/tenant`, never agent-name alone), the task input, and pre-bounded context assembled
by the supervisor; the provider MUST NOT assemble or expand context itself.

#### Scenario: Invocation carries a full address

- GIVEN a supervisor invokes its provider for a task belonging to agent `worker` under tenant
  `acme`
- WHEN the provider receives the invocation
- THEN the address it receives is the full `worker/acme`, never `worker` alone

#### Scenario: Provider does not enlarge received context

- GIVEN a supervisor invokes a provider with a context already capped at the declared budget
- WHEN the provider processes the invocation
- THEN the provider treats the received context as complete and does not request or assemble
  additional context beyond it

### Requirement: Providers Return Action Intents, Never Execute Actions Directly

When a provider's processing wants an external effect (for example, sending a message through a
gateway), it MUST return an action intent describing the desired effect and MUST NOT execute or
trigger that effect itself. Only the framework's risk policy may authorize execution.

#### Scenario: A provider wanting an external effect only declares intent

- GIVEN a provider, invoked for any agent role, determines during processing that it wants to
  trigger an outbound gateway action
- WHEN it returns its result
- THEN the result contains an action intent describing the desired action, and no external
  effect has occurred as a direct consequence of the provider's own processing

### Requirement: Providers Declare Their Capabilities

A conforming provider MUST expose a capability declaration including its context budget and the
action kinds it can emit as intents, queryable by the supervisor before or independently of any
specific invocation.

#### Scenario: Supervisor reads provider capabilities before assembling context

- GIVEN a supervisor is about to invoke a provider
- WHEN it queries the provider's capability declaration
- THEN it receives a context budget value it can use to cap the context it assembles

### Requirement: Providers Are Stateless Across Invocations

A provider MUST NOT retain a session identifier or other cross-invocation state as part of the
contract; continuity between invocations (for example, resuming after `INPUT_REQUIRED`) is
carried entirely by data the supervisor passes in on each invocation.

#### Scenario: Resume is expressed via input data, not a provider-held session

- GIVEN a task was previously escalated and approved, and the supervisor now resumes it
- WHEN the supervisor invokes the provider again
- THEN the resume information is passed as part of the invocation's input, and the provider
  requires no previously held session state to continue correctly

### Requirement: Provider Failures Map to a Terminal Outcome, Never a Silent Hang

Any provider-side failure (error, non-zero-equivalent outcome, or unrecoverable internal fault)
MUST be surfaced to the supervisor as a distinguishable failure outcome, never as an indefinite
pending state.

#### Scenario: A provider failure is surfaced, not swallowed

- GIVEN a provider invocation fails during processing for any agent's task
- WHEN the provider returns control to the supervisor
- THEN the supervisor receives an explicit failure outcome it can map to the task's `FAILED`
  state

### Requirement: Agent Names Are Consistent Across Config and Provider

The agent name used in `company.yaml` MUST be the same name passed to the provider adapter.
This ensures consistent identification across the framework and the provider's system.

For providers with named agents (e.g., OpenCode), the adapter passes the agent name to the
provider CLI via `--agent <name>`. For providers without named agents (e.g., Claude), the
agent name is not passed.

#### Scenario: OpenCode adapter passes agent name to CLI

- GIVEN an agent named `gentle-orchestrator` configured with provider `opencode`
- WHEN the supervisor invokes the OpenCode adapter
- THEN the adapter spawns `opencode run --agent gentle-orchestrator <input>`

#### Scenario: Claude adapter does not pass agent name

- GIVEN an agent named `ceo` configured with provider `claude-code`
- WHEN the supervisor invokes the Claude adapter
- THEN the adapter spawns `claude -p <input>` without an agent name flag
