# Archive Report: security-boundaries

**Change**: security-boundaries
**Archived**: 2026-09-09
**Archived to**: `openspec/changes/archive/2026-09-09-security-boundaries/`
**Artifact store**: hybrid (openspec + engram)
**Verdict**: PASS — SDD cycle complete

## Source Artifacts (Engram observation IDs)

| Artifact | Observation ID | Topic Key |
|----------|---------------|-----------|
| Explore | #1262 | sdd/security-boundaries/explore |
| Proposal | #1264 | sdd/security-boundaries/proposal |
| Spec | #1265 | sdd/security-boundaries/spec |
| Design | #1268 | sdd/security-boundaries/design |
| Tasks | #1270 | sdd/security-boundaries/tasks |
| Verify Report | #1289 | sdd/security-boundaries/verify-report |

## Final State (per Final-State Authority hierarchy)

**Task completion** (persisted tasks artifact, highest authority): 15/15 tasks `[x]` complete across Phase 1–4. No unchecked implementation tasks.

**Verification** (verify-report obs #1289, post-remediation pass): PASS. 5/5 requirements, 9/9 scenarios verified with passing runtime tests. 0 CRITICAL issues. 0 blockers.

**Commits**:
- `1f49ac9` — Phase 1: config & fail-fast start
- `6e84761` — Phase 2+3: authInterceptor + integration tests
- `721ebbe` — Phase 4: corrective tests (remediated 3 CRITICAL gaps from prior FAIL round)

**Branch**: `feat/security-boundaries`

**Test scope**: `go test ./transport/a2a/... ./core/supervisor/... ./cmd/company/... -count=1` → all PASS. Build: `go build ./...` → clean.

**Pre-existing unrelated failures** (not caused by this change, confirmed via diff): `adapters/claudecode` and `adapters/opencode` packages fail `go vet ./...` and have gofmt issues. 8 pre-existing unrelated files noted by gofmt -l; all 5 files touched by this change are gofmt-clean.

**Scale**: ~640 total lines (~424 Phase 1-3 + ~216 Phase 4 remediation), predominantly tests. Well within 400-line budget per PR.

## Specs Synced

| Domain | Action | Note |
|--------|--------|------|
| a2a-bearer-auth | Created (new main spec) | Full spec; no prior main spec existed. 4 requirements, 5 scenarios. |
| a2a-transport | Created (MODIFIED delta, no prior main spec) | Delta applied as-is since no existing `openspec/specs/a2a-transport/spec.md` existed. Contains `## MODIFIED Requirements` header — future formalization recommended. 1 modified requirement, 2 scenarios. |

**Warning**: `a2a-transport/spec.md` was a MODIFIED delta but no prior canonical spec existed. Per archive policy (no main spec → copy delta as-is), the file was copied mechanically. The `## MODIFIED Requirements` marker is preserved in the canonical spec; a future change should rewrite this as a clean full spec.

## Mechanical Copy Verification

### Spec sync: a2a-bearer-auth
```
diff -r openspec/changes/security-boundaries/specs/a2a-bearer-auth/spec.md <temp>
(empty — byte-identical)
```

### Spec sync: a2a-transport
```
diff -r openspec/changes/security-boundaries/specs/a2a-transport/spec.md <temp>
(empty — byte-identical)
```

### Archive folder move
```
git mv openspec/changes/security-boundaries openspec/changes/archive/2026-09-09-security-boundaries
diff -r /tmp/sdd-archive.WICUiE/source openspec/changes/archive/2026-09-09-security-boundaries
(empty — byte-identical)
```

All three `diff -r` readbacks are empty. Mechanical copy contract satisfied.

## Archive Contents Verified

- [x] proposal.md ✅
- [x] exploration.md ✅
- [x] specs/ ✅ (a2a-bearer-auth, a2a-transport)
- [x] design.md ✅
- [x] tasks.md ✅ (15/15 tasks complete, no unchecked `[ ]`)
- [x] verify-report.md ✅
- [x] Active changes directory no longer contains `security-boundaries`
- [x] `openspec/specs/a2a-bearer-auth/` and `openspec/specs/a2a-transport/` created as source of truth

## Change Summary

Introduced Bearer token authentication for the A2A transport layer. Previously all inbound A2A requests were trusted without authentication. After this change:

- `CompanyConfig.AuthTokenEnv` is required; server fails fast if absent
- `a2a.New()` accepts `authToken string` and returns an error on empty token
- `authInterceptor` runs first in the interceptor chain, rejecting requests with missing/wrong tokens before any handler executes
- Nil `ServiceParams` (internal calls) bypass auth and receive `User="internal"` identity
- Valid bearer tokens receive `User="caller"` identity via `NewAuthenticatedUser`
- `ListTasks` returns only tasks owned by the authenticated caller identity
- Agent Card declares `httpBearer` security scheme

## SDD Cycle Status

COMPLETE. All phases executed: explore → propose → spec → design → tasks → apply → verify → archive.
