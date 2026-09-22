# Peer Directory Specification

## Purpose

A directory mapping each agent role to its live A2A base URL, populated as each agent's server
binds during `materialize`, so the delegation port and A2A client can address a peer by role
without hardcoding topology.

## Requirements

### Requirement: The Peer Directory Maps Role to Live Base URL, Populated at Bind Time

As each agent's A2A server successfully binds during `materialize`, the composition root MUST
register that agent's role and its live base URL in the peer directory. The directory MUST
reflect only agents whose servers have actually finished binding — never a URL for a server
that has not yet started listening.

#### Scenario: A bound agent's role is registered with its live base URL

- GIVEN the `engineer` agent's A2A server has finished binding to a live address
- WHEN the composition root registers it
- THEN the peer directory maps role `engineer` to that server's actual base URL

#### Scenario: An agent whose server has not yet bound is absent from the directory

- GIVEN the `engineer` agent's server has not yet finished binding
- WHEN the peer directory is queried for role `engineer`
- THEN the role is absent, not present with an empty or placeholder URL

### Requirement: Lookup of an Unknown or Not-Yet-Bound Role Returns an Explicit Named Error

Looking up a role that is not declared in `company.yaml`, or a role that is declared but whose
server has not yet finished binding, MUST return an explicit, named error identifying the role
and the reason (unknown vs. not-yet-bound, when distinguishable). It MUST NOT read a nil map,
MUST NOT return a zero-value URL silently, and MUST NOT panic.

#### Scenario: Looking up an undeclared role returns a named "unknown role" error

- GIVEN `company.yaml` declares only `ceo` and `engineer` roles
- WHEN the peer directory is queried for role `"designer"`
- THEN it returns an explicit error naming `"designer"` as an unregistered role — never a
  zero-value URL, never a panic

#### Scenario: Looking up a declared-but-unbound role returns a named "not yet registered" error

- GIVEN `company.yaml` declares role `engineer`, but that agent's server has not yet finished
  binding during `materialize`
- WHEN the peer directory is queried for role `engineer` during that window
- THEN it returns an explicit "not yet registered" error naming `engineer` — never a nil-map
  read, never a panic

**Agent roles**: all roles declared in `company.yaml`. **Protocol**: in-process directory
consumed by the delegation port and the A2A client at wire time; not exposed over the wire
itself.
