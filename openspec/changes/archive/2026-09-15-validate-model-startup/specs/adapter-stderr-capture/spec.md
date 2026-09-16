# adapter-stderr-capture Specification

## Purpose

Ensures subprocess stderr is captured by adapters (claudecode, opencode) and surfaced in errors on non-zero exit, so callers receive actionable diagnostics rather than opaque exit codes.

Agent roles affected: all (CEO, CTO, engineer, designer) — all agent tasks are dispatched through these adapters.

## Requirements

### Requirement: Stderr Captured and Surfaced on Subprocess Failure

The adapter MUST capture subprocess stderr during every CLI invocation. On a non-zero exit code, the adapter MUST include the captured stderr text in the returned error message. On zero exit, the adapter MAY discard stderr.

Agent role protocol: applies to the claudecode and opencode adapter implementations, which back all agent roles.

#### Scenario: Subprocess fails with stderr output

- GIVEN an adapter CLI invocation where the subprocess exits non-zero
- WHEN the subprocess writes diagnostic text to stderr
- THEN the error returned to the caller MUST contain that stderr text

#### Scenario: Subprocess fails with empty stderr

- GIVEN an adapter CLI invocation where the subprocess exits non-zero
- WHEN the subprocess writes nothing to stderr
- THEN the error returned MUST include the exit code and MUST NOT panic or produce an empty-string artifact

#### Scenario: Subprocess succeeds

- GIVEN an adapter CLI invocation where the subprocess exits zero
- WHEN the subprocess writes any content to stderr
- THEN no error is returned and stderr MAY be silently discarded
