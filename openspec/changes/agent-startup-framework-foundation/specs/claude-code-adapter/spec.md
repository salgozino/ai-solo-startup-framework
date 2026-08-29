# Claude Code Adapter Specification

## Purpose

The Claude Code adapter is the first concrete implementation of the provider adapter contract.
It conforms to `provider-adapter` in full; this spec covers only the behavior specific to
running Claude Code as the provider, not a restatement of the generic contract.

## Requirements

### Requirement: Conformance to the Provider Contract

The Claude Code adapter MUST satisfy every requirement in the `provider-adapter` specification:
full-address invocation, action-intent-only effects, capability declaration, statelessness
across invocations, and failure mapping.

#### Scenario: Claude Code adapter passes the same conformance checks as any provider

- GIVEN a test suite written against the `provider-adapter` contract with no Claude
  Code-specific assumptions
- WHEN it is run against the Claude Code adapter
- THEN it passes without any test needing adapter-specific changes

### Requirement: One Ephemeral Process Per Invocation

The Claude Code adapter MUST start a fresh process for each invocation and MUST NOT reuse a
process or session across invocations, for any agent role.

#### Scenario: Two invocations for the same agent use two separate processes

- GIVEN an agent's supervisor invokes the Claude Code adapter twice for two different tasks
- WHEN both invocations run
- THEN each runs in its own freshly started process with no shared in-process state between
  them

### Requirement: Output Is Parsed into A2A Parts Before Returning

The adapter MUST parse the underlying Claude Code process output into the provider contract's
`Part` representation before returning a result; raw process output MUST NOT be exposed through
the port.

#### Scenario: Raw CLI output never crosses the port boundary

- GIVEN the underlying Claude Code process produces its native output format
- WHEN the adapter returns the invocation result
- THEN the result contains parsed `Part` values, and no raw unparsed process output is present
  in the returned result

### Requirement: Non-Zero Exit Maps to a Failure Outcome

A non-zero (or otherwise failing) exit from the underlying Claude Code process MUST be mapped by
the adapter to the provider contract's failure outcome.

#### Scenario: Non-zero exit becomes a mapped failure

- GIVEN the underlying Claude Code process exits with a non-zero status for a task
- WHEN the adapter completes handling that invocation
- THEN the adapter returns the contract's failure outcome, not a success result with a hidden
  error

### Requirement: Subprocess Invocation Is Isolated From Argument Injection

The adapter MUST invoke the underlying process using an argument list, never a
shell-interpolated string, so that task input cannot inject additional arguments or commands.

#### Scenario: Task input containing shell metacharacters does not alter the invocation

- GIVEN a task's input text contains shell metacharacters (for example, `; rm -rf`)
- WHEN the adapter invokes the underlying process with that input
- THEN the metacharacters are passed as literal input data and do not add, remove, or alter any
  process argument

### Requirement: Hung Processes Are Terminated, Not Left Running

The adapter MUST enforce a deadline on the underlying process and terminate it if the deadline
is exceeded, returning a failure outcome rather than blocking indefinitely.

#### Scenario: A hung process is killed and reported as failed

- GIVEN the underlying Claude Code process exceeds its invocation deadline without exiting
- WHEN the deadline elapses
- THEN the adapter terminates the process and returns a failure outcome for that invocation
