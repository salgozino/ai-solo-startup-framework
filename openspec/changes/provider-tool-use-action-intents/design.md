# Design: Provider Tool Use and Action Intent Emission via Native MCP

## Technical Approach

One long-lived MCP server (`transport/mcp`) mounts on a loopback `http.Server` in `cmd/company`, registering one tool per `config.Policy` key. Adapters mint a per-invocation token, hand the CLI an ephemeral MCP config, and after exit drain that token's intents into `ProviderResult.ActionIntents`. Stream parsing drops to text extraction. Auth layers as in `transport/a2a`: bearer, then tenant.

## Architecture Decisions

### Decision A: Use the official `modelcontextprotocol/go-sdk`

| Option | Tradeoff | Verdict |
|---|---|---|
| **`go-sdk` v1.8.0** (official; 2026-09-04; `v1.x` semver promise) | +1 dep; supplies `NewStreamableHTTPHandler`, `auth.RequireBearerToken`, `req.Extra.TokenInfo`, `AddTool`, **and an MCP client for tests** | **Chosen** |
| Hand-rolled JSON-RPC (`initialize`/`tools/list`/`tools/call`) | Zero deps, but those three are not the real surface: Streamable HTTP also needs `notifications/initialized`, version negotiation, `Mcp-Session-Id`, SSE upgrade | Rejected |

**Rationale**: both clients are third-party and unpatchable, and CLI evidence (Engram `cli-evidence/claude-opencode-tool-use`) proved they diverge on everything observable — hand-rolling means debugging our transport against two black boxes. Version risk is bounded: GA `v1.x`, official org, pinned. Precedent: `a2a-go/v2`.

### Decision B: Server-owned `Registry` keyed by opaque token

`TokenInfo.Expiration` and `UserID` are middleware-enforced, so TTL and session-pinning are not hand-rolled.

| Concern | Design |
|---|---|
| Token | 32 bytes `crypto/rand`, base64url. Entropy makes map lookup safe; a2a's `ConstantTimeCompare` guards a *static shared* secret, this one is per-invocation and single-use |
| Lifecycle | `Mint(tenant,agent,taskID)` before spawn → resolve per call → `Drain()` after exit, deleting the entry |
| Concurrency | `map[string]*invocation` under `sync.RWMutex`; per-invocation intents under their own mutex. Parallel agents make this load-bearing; `-race` covers it |
| Expiry | `Expiration = deadline + grace`; middleware rejects before any handler |
| Finished invocation | Entry deleted on drain → token unknown → rejected identically. Late calls cannot mutate a returned result |

### Decision C: Acknowledge-without-execute (highest risk)

The inversion steers the model twice — **before** the call via description, **after** via result.

- **Description** (spec-mandated): records an intent; does not perform the action; no result observable this turn; call once.
- **Result**: `isError: false` (an error triggers retry). Text: *"Recorded intent `<kind>` as `<receipt>`. Pending classification; outcome unavailable this turn. Do not call again. Report as requested, not completed."* Plus `StructuredContent{receipt, kind, status}`.
- **Dedupe**: key `token + kind + canonical(payload)`. A repeat returns the same receipt with `duplicate:true` and records nothing — the proposal's retry risk becomes a tested invariant.

## Data Flow

    Supervisor ─RunTask─→ Adapter ─Mint─→ Registry
                             │               ↑ resolve {tenant,agent,task}
                             ├─spawn CLI─→ tools/call ──┤
                             │                          ↓
                             └─Drain──────────────── Sink ──→ ActionIntents
                                                                  │
                                         PolicyEngine ←───────────┘ → Gateway (later, never here)

## File Changes

| File | Action | Description |
|---|---|---|
| `core/port/provider.go` | Modify | `ProviderCapabilities`; `Capabilities()` on `Provider` |
| `core/port/fake/fake_provider.go` | Modify | Canned `Capabilities()` |
| `transport/mcp/{server,registry,sink,tools}.go` | Create | Handler, registry, sink, policy-derived tools |
| `adapters/{claudecode,opencode}/adapter.go` | Modify | Mint/drain, ephemeral config, text-only parsing |
| `adapters/*/testdata/fake*/main.go` | Modify | Fake binaries become real MCP clients |
| `core/supervisor/supervisor.go` | Modify | `Config.ContextBudget` becomes override |
| `cmd/company/{wire,main}.go`, `go.mod` | Modify | Server lifecycle before `materializeAgents`; SDK dep |

## Interfaces / Contracts

```go
type ProviderCapabilities struct { ContextBudget int; ActionKinds []string }

func (r *Registry) Mint(tenant, agent, taskID string, exp time.Time) (string, *Handle)
func (h *Handle) Drain() []port.ActionIntent // deletes entry; idempotent
```

## Delivery Slices

| # | Scope | Can | Cannot | Intermediate state |
|---|---|---|---|---|
| 1 | port + capabilities | Honor budget override | Emit intents | New method breaks all 3 implementers at once; `var _ port.Provider` catches it |
| 2 | MCP server + auth | Full `tools/call` tested via the SDK's own client — no CLI, no wiring | Reach an adapter | Unreferenced package; build green |
| 3 | adapters + wiring | End-to-end intents | — | Loop closed |

## Testing Strategy

The gap: policy→escalation→gateway is proven only through `fake.Provider`, so both real adapters could be silent no-ops with every test green. Strict TDD — RED first.

| Layer | What | How |
|---|---|---|
| Unit | Budget precedence; dedupe; mint/resolve/drain | Table tests |
| Unit | Registry under parallel invocations | Concurrent goroutines, `-race` (already in CI) |
| Integration | Unknown, expired, drained, foreign-tenant → reject, **zero** intents | SDK client against `httptest` server |
| Integration | `tools/call` never reaches `Gateway.Send` | Spy gateway asserts zero calls |
| Adapter | **Closes the gap**: fake `claude`/`opencode` binaries make a real `tools/call` over HTTP; assert non-empty `ActionIntents` | Existing `helperBinary` pattern, extended. No billable calls |
| Adapter | No tool call → empty intents, nil error; persisted config mtime unchanged | Same doubles |
| E2E | Intent → classified | Existing `cmd/company/e2e_test.go` |

## Threat Matrix

| Boundary | Applicability | Design response | Planned RED test |
|---|---|---|---|
| Documentation-like paths | N/A — no file classification or execution | — | — |
| Git selection / commit / push / PR commands | N/A — no VCS or PR automation | — | — |
| Subprocess argv | Applicable | Token in an HTTP header, never argv/`ps`; argv stays a slice | Token absent from argv |
| Ephemeral config | Applicable | Temp file 0600 + `--strict-mcp-config`; `OPENCODE_CONFIG_CONTENT` scoped to that process env | Persisted mtime unchanged; temp file removed |
| Network exposure | Applicable | Bind `127.0.0.1:0`; bind failure aborts `materialize` before any supervisor is ready | Bind failure aborts, no adapter invoked |
| Unbounded sink | Applicable | Per-invocation intent cap; excess rejected | Cap enforced |

## Migration / Rollout

No migration; no persisted format changes. Rollback = revert.

## Open Questions

- [ ] `opencode --format json` version floor unverified upstream — document "1.18.31 verified" (from proposal; non-blocking).
