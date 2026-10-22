# Proposal: pi Provider Adapter (Default Provider)

## Intent

Add the pi coding agent CLI as a third provider backend, next to Claude Code and OpenCode, and
make it the default: an agent that omits `provider` runs on pi. Tracking: `odd/tasks/pi-adapter.md`.

## Scope

### In Scope
- `adapters/pi`: `port.Provider` over `pi -p --mode json`, input on stdin, system prompt via
  `--append-system-prompt <path>`, `ProbeModel` via `--list-models`.
- Ephemeral MCP wiring through a temp extension calling `pi.registerMcpServer`, token only in
  subprocess env.
- Isolation: `-ne` and no skills/templates/context files/project files/session; `--tools`
  allowlist when MCP is wired.
- `company.yaml`: `provider` defaults to `pi`; new pi-only per-agent `extensions` allowlist.
- `cmd/company` wiring; README, AGENTS.md, example company.yaml.

### Out of Scope
- Installing pi or pi packages; `npm:`/`git:` sources in `extensions` (pi ignores them on `-e`).
- Exposing tools registered by allowlisted extensions to the agent.
- pi RPC mode or SDK embedding.

## Capabilities

### New Capabilities
- `pi-provider-adapter`: pi invocation contract, failure detection, MCP delivery, isolation,
  extensions allowlist, default provider.

## Risks
- pi exits 0 on model failure; mitigated by parsing the final assistant `stopReason`.
- Ambient `mcp.json` servers still connect under `-ne`; mitigated by the `--tools` allowlist.
- No CLI flag disables ambient MCP connections entirely; documented as accepted tradeoff.
