```yaml
schema: gentle-ai.verify-result/v1
evidence_revision: sha256:9c447e9103c26d561b64e30da57518321cd7aa851e8d43930f9a8eda63b7e135
verdict: fail
blockers: 2
critical_findings: 2
requirements: 43/49
scenarios: 53/59
test_command: go test ./... -count=1 -race
test_exit_code: 0
test_output_hash: sha256:37a0794e9829c22bdd3dde01f6c7a5f5acd9ced2e1495d673bb24a921f122c35
build_command: go build ./...
build_exit_code: 0
build_output_hash: sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

## Verification Report

**Change**: agent-startup-framework-foundation
**Version**: N/A (delta specs, 9 capabilities)
**Mode**: Strict TDD (runner: `go test ./...`)
**Repository revision**: `7efab3f` (branch `master`)

### Completeness

| Metric | Value |
|--------|-------|
| Tasks total | 51 |
| Tasks complete | 51 |
| Tasks incomplete | 0 |
| Requirements total | 49 |
| Requirements satisfied | 43 |
| Scenarios total | 59 |
| Scenarios compliant | 53 |

Task completion is objectively 51/51 (`- [x]` = 51, `- [ ]` = 0). Task completion is **not**
equivalent to requirement satisfaction: two spec requirements are not satisfied despite every
task being checked (see CRITICAL findings).

### Build & Tests Execution

**Build**: ✅ Passed

```text
$ go build ./...
(no output)
exit code: 0
```

**Static analysis**: ✅ Passed

```text
$ go vet ./...
(no output)
exit code: 0

$ gofmt -l .
(no output)
```

**Tests**: ✅ 145 top-level tests passed / 0 failed / 1 skipped (222 assertions-level pass events,
2 skip events including subtests)

```text
$ go test ./... -count=1 -race
ok  	github.com/salgozino/ai-solo-startup-framework/adapters/claudecode	4.558s
ok  	github.com/salgozino/ai-solo-startup-framework/adapters/opencode	3.426s
ok  	github.com/salgozino/ai-solo-startup-framework/cmd/company	1.112s
ok  	github.com/salgozino/ai-solo-startup-framework/config	1.028s
ok  	github.com/salgozino/ai-solo-startup-framework/core/address	1.017s
ok  	github.com/salgozino/ai-solo-startup-framework/core/policy	1.019s
ok  	github.com/salgozino/ai-solo-startup-framework/core/port	1.019s
?   	github.com/salgozino/ai-solo-startup-framework/core/port/fake	[no test files]
ok  	github.com/salgozino/ai-solo-startup-framework/core/supervisor	1.069s
ok  	github.com/salgozino/ai-solo-startup-framework/gateways/telegram	1.034s
ok  	github.com/salgozino/ai-solo-startup-framework/transport/a2a	1.144s
ok  	github.com/salgozino/ai-solo-startup-framework/ui	1.216s
exit code: 0
```

Race detector reported no data races. The single skip is
`gateways/telegram.TestIntegration_LiveSend`, gated on `TELEGRAM_BOT_TOKEN` and
`TELEGRAM_OWNER_ID` being present; this is a deliberate live-network guard, not a masked failure.

**Coverage**: 74.2% of statements (project total) / threshold: 0% configured → ✅ Above
configured threshold.

### Prior Unremediated Failed Attempt — Reproduction Result

The native attempt ledger records a prior unremediated failed verification bound to evidence
`sha256:2417354617c935efe01248b93f9bd62d9e7488f970014434cfeb6156feed2746`. No
`verify-report.md` for this change has ever existed in git history, consistent with a validator
denial that correctly produced zero writes.

Runtime reproduction result: **the earlier failure does not reproduce as a command failure.**
All three declared verification commands exit `0`. The failure instead reproduces as
**requirements-level non-compliance** that command exit codes cannot detect.

Partial remediation evidence found in history: commits `a815916`
(`fix(a2a): enforce tenant match against supervisor's own bound tenant`), `6a251e5`
(`docs(a2a): update tenant specs to reflect enforced security boundary`) and `3715efc`
(`test(a2a): consolidate and strengthen tenant interceptor coverage`) remediated an A2A tenant
boundary hole. That defect is now covered at runtime by
`TestTenantInterceptor_Before_BoundToSupervisorOwnTenant` and
`TestAuthPrecedesTenantValidation`, both passing. Two other requirement defects were **not**
remediated and are reported below as CRITICAL.

### Spec Compliance Matrix

| Requirement | Scenario | Test | Result |
|-------------|----------|------|--------|
| a2a: Discoverable Agent Card | Newly started supervisor is discoverable | `transport/a2a > TestAgentCardDiscoverable` | ✅ COMPLIANT |
| a2a: Real A2A Transport | CEO delegates to worker over the real wire | `core/supervisor > TestIntegration_CEODelegatesToWorkerOverRealWire` | ✅ COMPLIANT |
| a2a: Core Task Operations | A client lists tasks for a supervisor | `core/supervisor > TestIntegration_... (ListTasks)`, `cmd/company > TestMaterialize_TwoAgentStartsTwoGoroutines` | ✅ COMPLIANT |
| a2a: Tenant Carried and Validated | Request matching supervisor's own tenant accepted | `transport/a2a > TestTenantInterceptor_Before_BoundToSupervisorOwnTenant` | ✅ COMPLIANT |
| a2a: Tenant Carried and Validated | Empty tenant rejected at the edge | `transport/a2a > TestTenantInterceptor_Before` | ✅ COMPLIANT |
| a2a: Tenant Carried and Validated | Request claiming a different tenant rejected | `transport/a2a > TestTenantInterceptor_Before_BoundToSupervisorOwnTenant` | ✅ COMPLIANT |
| a2a: Push Notifications Stream | State transition appears on the stream without polling | `ui > TestSSEReceivesEvent`, `ui > TestSSEBroadcastEventType` | ⚠️ PARTIAL |
| supervisor: Lifecycle States | Reaches IDLE after startup | `core/supervisor > TestFSM_StartingToIdle` | ✅ COMPLIANT |
| supervisor: Lifecycle States | Returns to IDLE after task completion | `core/supervisor > TestFSM_IdleToWorkingToIdle` | ✅ COMPLIANT |
| supervisor: IDLE Observable | Monitoring UI observes a genuinely idle agent | `transport/a2a > TestSupervisorStatusIdle` | ✅ COMPLIANT |
| supervisor: TaskState Progression | Full escalation cycle traversal | `core/supervisor > TestSupervisor_EscalationCycle` | ✅ COMPLIANT |
| supervisor: TaskState Progression | Provider failure marks the task FAILED | `transport/a2a > TestProviderFailureMarksFailed` | ✅ COMPLIANT |
| supervisor: Crash/Restart Recovery | Restart after a mid-task crash | `core/supervisor > TestRecoverOpenTasks_DoubleRecoveryIsIdempotent`, `transport/a2a > TestRecoveredTask_ReachableOverHTTP` | ✅ COMPLIANT |
| supervisor: Crash/Restart Recovery | Restart preserves a parked INPUT_REQUIRED task | `core/supervisor > TestSupervisor_RestartPreservesInputRequired` | ✅ COMPLIANT |
| supervisor: Bounded Context Assembly | Context within budget passes through unmarked | `core/supervisor > TestAssembleBoundedContext_WithinBudget` | ✅ COMPLIANT |
| supervisor: Bounded Context Assembly | Oversized context truncated with visible marker | `core/supervisor > TestAssembleBoundedContext_OverBudget` | ✅ COMPLIANT |
| supervisor: One Supervisor One Identity | Two agents in one company never share a queue | `core/supervisor > TestStore_TenantIsolation`, `cmd/company > TestMaterialize_TwoAgentStartsTwoGoroutines` | ⚠️ PARTIAL |
| approval: INPUT_REQUIRED not AUTH_REQUIRED | Risky permitted action transitions to INPUT_REQUIRED | `core/policy > TestClassify_AllowedRiskyRole`, `core/supervisor > TestSupervisor_EscalationCycle` | ✅ COMPLIANT |
| approval: Escalated Task Survives Restart | Escalated task still resumable after restart | `core/supervisor > TestSupervisor_RestartPreservesInputRequired`, `TestIntegration_InputRequiredRecoveredAndResumed` | ✅ COMPLIANT |
| approval: Resume Carries Same Task ID | Approval resumes the correct parked task | `core/supervisor > TestSupervisor_ResumeRecognized`, `core/policy > TestResumeFlow_PolicyEngine` | ✅ COMPLIANT |
| approval: Exactly One Effect | Approval sends the message exactly once | `cmd/company > TestE2E_ApproveFlow_CEO_TelegramSend`, `core/supervisor > TestSupervisor_ResumeBodyPropagated` | ✅ COMPLIANT |
| approval: Zero Effect on Rejection | Rejection prevents the send entirely | `cmd/company > TestE2E_RejectFlow_CEO_TelegramSend`, `core/supervisor > TestSupervisor_RejectionPreventsGateway` | ✅ COMPLIANT |
| approval: Versioned Payload | Unrecognized payload version not blindly trusted | `core/policy > TestPayload_UnrecognizedVersion`, `TestPayload_MalformedJSON`, `TestPayload_MarshalValidate` | ✅ COMPLIANT |
| claude: Provider Contract Conformance | Passes the same conformance checks as any provider | `adapters/claudecode > TestContract_RunTask_Stateless`, `TestContract_RunTask_OutputParsed`, `TestContract_RunTask_FailureIsMapped` | ✅ COMPLIANT |
| claude: One Ephemeral Process Per Invocation | Two invocations use two separate processes | `adapters/claudecode > TestInvocation_SeparateInstances` | ✅ COMPLIANT |
| claude: Output Parsed into A2A Parts | Raw CLI output never crosses the port boundary | `adapters/claudecode > TestInvocation_RawOutputNeverExposed` | ✅ COMPLIANT |
| claude: Non-Zero Exit Maps to Failure | Non-zero exit becomes a mapped failure | `adapters/claudecode > TestNonZeroExit_MapsToError`, `TestInvocation_NonZeroExitTable` | ✅ COMPLIANT |
| claude: Isolated From Argument Injection | Shell metacharacters do not alter the invocation | `adapters/claudecode > TestArgvSlice_ShellMetacharactersAreLiteral` | ✅ COMPLIANT |
| claude: Hung Processes Terminated | Hung process killed and reported as failed | `adapters/claudecode > TestHungChild_KilledOnDeadline` | ✅ COMPLIANT |
| company: Declares Tenant/Agents/Gateways/Policy | Minimal valid company file accepted | `config > TestLoad`, `cmd/company > TestLoad_AcceptsEnvVarRef` | ✅ COMPLIANT |
| company: CLI Materializes Topology | Two-agent company starts two supervisors | `cmd/company > TestMaterialize_TwoAgentStartsTwoGoroutines` | ✅ COMPLIANT |
| company: Secrets by Env Var | Inline token rejected at load time | `cmd/company > TestLoad_RejectsInlineToken`, `gateways/telegram > TestNew_InlineTokenRejected` | ✅ COMPLIANT |
| company: Gateways Declared at Company Level | Agent-level gateway field rejected or ignored | `config > TestLoad/agent-level gateway field is rejected` | ✅ COMPLIANT |
| company: Second Tenant Without Interference | Two tenants coexist after materialization | `cmd/company > TestE2E_MultiTenantIsolation` | ✅ COMPLIANT |
| ui: State Displayed Read-Only | Viewer sees current state for all agents | `ui > TestGetRoot`, `TestGetTasks` | ✅ COMPLIANT |
| ui: Approve/Reject Only for Pending | Completed task offers no approve/reject control | `ui > TestApproveNonInputRequired` | ✅ COMPLIANT |
| ui: Approve/Reject Only for Pending | Pending escalation offers approve/reject | `ui > TestApproveInputRequired`, `TestRejectInputRequired` | ✅ COMPLIANT |
| ui: Cannot Pause/Kill/Reassign/Command | No control exists to stop a running agent | `ui > TestSendTaskSuccess` (contradicts) | ❌ FAILING |
| ui: Updates Without Polling | Escalation appears live without manual refresh | `ui > TestSSEReceivesEvent`, `TestSSEBroadcastEventType` | ✅ COMPLIANT |
| provider: Full Address and Bounded Context | Invocation carries a full address | `core/port > TestProvider_SendTask_FullAddress` | ✅ COMPLIANT |
| provider: Full Address and Bounded Context | Provider does not enlarge received context | (none found) | ❌ UNTESTED |
| provider: Action Intents Never Direct Execution | Provider wanting an effect only declares intent | `core/port > TestGateway_Send_NonInvocation`, `core/supervisor > TestSupervisor_HardDeny_ProducesRejected` | ✅ COMPLIANT |
| provider: Declare Their Capabilities | Supervisor reads provider capabilities before assembling context | (none found — not implemented) | ❌ UNTESTED |
| provider: Stateless Across Invocations | Resume via input data, not provider-held session | `adapters/claudecode > TestContract_RunTask_Stateless`, `core/supervisor > TestSupervisor_ResumeRecognized` | ✅ COMPLIANT |
| provider: Failures Map to Terminal Outcome | Provider failure surfaced, not swallowed | `adapters/claudecode > TestContract_RunTask_FailureIsMapped`, `transport/a2a > TestProviderFailureMarksFailed` | ✅ COMPLIANT |
| provider: Agent Names Consistent | OpenCode adapter passes agent name to CLI | `adapters/opencode > TestAgentFlag_PassedToCLI`, `TestNoAgentFlag_OmitsFlag` | ✅ COMPLIANT |
| provider: Agent Names Consistent | Claude adapter does not pass agent name | (none found) | ❌ UNTESTED |
| policy: Capability Precedes Risk | Capability check runs first for every intent | `core/policy > TestClassify_DisallowedRole` (risky + disallowed role → HardDeny, no escalation) | ✅ COMPLIANT |
| policy: Disallowed Role Hard-Denied | Disallowed role produces rejection with no escalation | `core/policy > TestClassify_DisallowedRole`, `core/supervisor > TestSupervisor_HardDeny_ProducesRejected` | ✅ COMPLIANT |
| policy: Allowed Risky Escalates | CEO's risky, permitted send escalates | `core/policy > TestClassify_AllowedRiskyRole` | ✅ COMPLIANT |
| policy: Allowed Non-Risky Executes | Non-risky permitted action does not wait for a human | `core/policy > TestClassify_AllowedNonRiskyRole` | ✅ COMPLIANT |
| policy: Denial Provably Unreachable | Hard-denied intent leaves no trace of an attempted send | `core/policy > TestClassify_HardDenyIsNotFailed`, `cmd/company > TestE2E_HardDeny_WorkerTelegramSend` | ✅ COMPLIANT |
| policy: Rules Declared Once Per Action Kind | Single declaration governs all agents for an action kind | `core/policy > TestClassify_*` (one `telegram_send` declaration applied to `ceo` and `engineer`) | ✅ COMPLIANT |
| telegram: Outbound Only | No inbound message is ever processed | `gateways/telegram > TestContract_TelegramGateway_*` (Gateway exposes no inbound method) | ✅ COMPLIANT |
| telegram: Recipient From Configuration | Action intent carrying its own recipient is ignored | `gateways/telegram > TestSend_OwnerWinsOverCallerRecipient`, `cmd/company > TestE2E_CallerRecipientIgnored` | ✅ COMPLIANT |
| telegram: Reachable Only Via Authorized Action | Hard-denied intent never reaches Send | `cmd/company > TestE2E_HardDeny_WorkerTelegramSend` | ✅ COMPLIANT |
| telegram: Reachable Only Via Authorized Action | Rejected escalation never reaches Send | `cmd/company > TestE2E_RejectFlow_CEO_TelegramSend`, `core/supervisor > TestSupervisor_RejectionPreventsGateway` | ✅ COMPLIANT |
| telegram: Send Failure Fails Task | Failed send fails the task, not the supervisor | `gateways/telegram > TestSend_APIFailure`, `TestContract_TelegramGateway_DeliveryFailure` | ✅ COMPLIANT |
| telegram: Bot Token via Environment | Gateway with no environment token fails closed | `gateways/telegram > TestNew_NoToken`, `TestNew_NoOwner` | ✅ COMPLIANT |

**Compliance summary**: 53/59 scenarios compliant, 2 PARTIAL, 3 UNTESTED, 1 FAILING.

### Correctness (Static Evidence)

| Requirement | Status | Notes |
|------------|--------|-------|
| `provider-adapter`: Providers Declare Their Capabilities | ❌ Not implemented | `port.Provider` has no capability-declaration method. No `ProviderCapabilities` type exists anywhere in the module. `core/port/provider.go:101` carries a dangling doc comment referencing it. |
| `monitoring-ui`: UI Cannot Command an Agent | ❌ Violated | `ui/handler.go:96` registers `POST /api/send` → `handleSendTask`, which calls `sup.SendTask(...)`. This is a second UI write action that commands an agent to start work. |
| `agent-supervisor`: Bounded Context Assembly | ✅ Implemented | `assembleBoundedContext` in `core/supervisor/context.go`, called at `supervisor.go:411`, capped by `supervisor.Config.ContextBudget` (not by a provider declaration — see finding C-1). |
| `a2a-transport`: Tenant Validation | ✅ Implemented | `transport/a2a/server.go` validates the request tenant against the supervisor's own bound tenant; auth precedes tenant validation. |
| `port.ResumePoint` | ⚠️ Dead code | Declared in `core/port/provider.go:120`, never referenced by any non-test production code. Resume is carried as plain input instead. |

### Coherence (Design)

| Decision | Followed? | Notes |
|----------|-----------|-------|
| Core never imports adapters/gateways (ports-and-adapters seam) | ✅ Yes | Only `cmd/company` wires concretes; verified by import inspection. |
| One supervisor owns one agent identity, keyed by `agent-name/tenant` | ✅ Yes | `Store` is keyed by full `address.A2AAddress`; endpoints are distinct per supervisor. |
| Bounded context is a typed `port.BoundedContext` crossing the provider boundary | ⚠️ Deviation | `BoundedContext` is assembled then flattened to a plain string by `contextText()` before `RunTask(ctx, taskID, input string)`. Behaviour is preserved; the typed contract is not. |
| Provider declares its own context budget | ❌ No | Budget is supervisor configuration, inverting the declared direction of the contract. |
| Monitoring UI is read-only except approve/reject | ❌ No | `POST /api/send` added by `95f8b82` with no authorizing spec. |
| Push notification stream (SSE) offered by each supervisor's A2A transport | ⚠️ Deviation | SSE lives in `ui/handler.go` (`/api/events`) with a `Broadcast` hook from `core/supervisor`; it is not surfaced by `transport/a2a`. |

### Strict TDD Sections

#### TDD Compliance

| Check | Result | Details |
|-------|--------|---------|
| TDD Evidence reported | ➖ Unavailable | No `apply-progress` artifact exists for this change (`artifactPaths.applyProgress: []`). The TDD Cycle Evidence table cannot be validated. Absence is expected for this change and is not treated as a failure. |
| All tasks have tests | ⚠️ Partial | 51/51 tasks checked, but 3 spec scenarios have no covering test and 1 is contradicted by a test. |
| RED confirmed (tests exist) | ✅ | Test files exist for every package under verification; `core/policy/policy_test.go` header explicitly records "Task 4.1 RED: these tests are written before any production code." |
| GREEN confirmed (tests pass) | ✅ | 145/145 non-skipped top-level tests pass under `-race`. |
| Triangulation adequate | ✅ | Table-driven triangulation present in `TestTenantInterceptor_Before_BoundToSupervisorOwnTenant`, `TestInvocation_NonZeroExitTable`, `config > TestLoad`, `core/policy > TestClassify_*`. |
| Safety Net for modified files | ➖ Unverifiable | No `apply-progress` "Files Changed" table available. |

**TDD Compliance**: 3/6 checks passed, 2 unavailable, 1 partial.

#### Test Layer Distribution

| Layer | Tests | Files | Tools |
|-------|-------|-------|-------|
| Unit | ~104 | 14 | `go test` |
| Integration | ~34 | 5 (`transport/a2a/server_test.go`, `core/supervisor/integration_test.go`, `ui/handler_test.go`, `gateways/telegram/*_test.go`, `adapters/*/contract_test.go`) | `go test` + `httptest` |
| E2E | 8 | 2 (`cmd/company/e2e_test.go`, `cmd/company/cmd_test.go`) | `go test` + in-process A2A servers |
| **Total** | **146** | **21** | |

All integration and E2E tests are correctly gated with `testing.Short()` per repository convention.

#### Changed File Coverage

| Package | Line % | Rating |
|---------|--------|--------|
| `transport/a2a` | 97.6% | ✅ Excellent |
| `config` | 95.2% | ✅ Excellent |
| `core/policy` | 95.0% | ✅ Excellent |
| `core/port` | 100.0% | ✅ Excellent |
| `gateways/telegram` | 94.3% | ✅ Excellent |
| `adapters/claudecode` | 91.7% | ✅ Excellent |
| `adapters/opencode` | 87.2% | ✅ Excellent |
| `core/address` | 86.7% | ✅ Excellent |
| `ui` | 82.9% | ⚠️ Acceptable |
| `core/supervisor` | 77.9% | ⚠️ Low |
| `cmd/company` | 41.7% | ⚠️ Low |
| `core/port/fake` | 0.0% | ➖ Test doubles, no test files (expected) |

**Project coverage**: 74.2% of statements.

#### Assertion Quality

No tautologies, no `expect(true)`-class assertions, no orphan empty-collection assertions, no
ghost loops, and no assertions that never call production code were found. No `TODO`, `FIXME`,
`XXX`, or `HACK` markers exist in any Go file. No commented-out assertions were found. The 22
`t.Skip` call sites are all legitimate `testing.Short()` or missing-external-dependency guards,
not disabled assertions.

**Assertion quality**: ✅ All assertions verify real behavior.

#### Quality Metrics

**Linter (`go vet`)**: ✅ No errors
**Formatter (`gofmt -l`)**: ✅ No unformatted files
**Race detector**: ✅ No data races

### Issues Found

**CRITICAL**

- **C-1 — `provider-adapter` requirement "Providers Declare Their Capabilities" is not
  implemented.** The spec requires a provider to expose a queryable capability declaration
  including its context budget and emittable action kinds. `port.Provider`
  (`core/port/provider.go`) declares only `Complete`, `CompleteError`, `SendMessage`,
  `SendMessageStream`, `ResolveAgent`, `SendTask`, `RunTask` — no capability method. The type
  `ProviderCapabilities` does not exist anywhere in the module; `core/port/provider.go:101`
  contains a stale doc comment ("pre-capped by the supervisor according to
  `ProviderCapabilities.ContextBudget`") referencing it. The budget is instead
  `supervisor.Config.ContextBudget` (`core/supervisor/supervisor.go:29`), inverting the declared
  ownership. Scenario "Supervisor reads provider capabilities before assembling context" is
  `UNTESTED` — grep for `Capabilit` across all `_test.go` files returns zero matches.
  Corroborating stale evidence: task 3.6 (marked `[x]`) states the cap comes from
  `ProviderCapabilities.ContextBudget`, and `tasks.md:194` claims "All 9 capability specs
  checked. No requirement left without a task" — yet no task covers this requirement.

- **C-2 — `monitoring-ui` requirement "The UI Cannot Pause, Kill, Reassign, or Command an Agent"
  is violated by the implementation.** The requirement states "its only write action is recording
  an approve or reject verdict on a pending escalation" and the scenario states "the only write
  action anywhere in the UI is an approve/reject verdict on a pending escalation".
  `ui/handler.go:96` registers `POST /api/send` → `handleSendTask`, which at `ui/handler.go:246`
  calls `h.sup.SendTask(req.Message)` — a second write action that commands an agent to begin
  work. It is exercised and asserted as correct behaviour by `ui > TestSendTaskSuccess` and
  `ui > TestSendTaskEmptyMessage`, so the test suite actively encodes the violation. Introduced
  by commit `95f8b82` (`feat(ui): add send task to CEO from monitoring UI`) with no authorizing
  spec requirement in any change under `openspec/changes/`. This is a spec/implementation
  contradiction: either the requirement must be amended through a spec change, or `/api/send`
  must be removed. Verification cannot choose; escalating to the orchestrator.

**WARNING**

- **W-1** — `provider-adapter` scenario "Provider does not enlarge received context" has no
  covering test. Structurally the provider cannot enlarge context because `RunTask` receives a
  pre-flattened string, but the property is unasserted.
- **W-2** — `provider-adapter` scenario "Claude adapter does not pass agent name" has no covering
  test. `adapters/opencode` has both `TestAgentFlag_PassedToCLI` and `TestNoAgentFlag_OmitsFlag`;
  `adapters/claudecode` has no equivalent negative assertion.
- **W-3** — `a2a-transport` requirement "Push Notifications Stream Task State in Real Time" states
  "Each supervisor MUST offer a push notification stream (SSE)". SSE is implemented in the `ui`
  package (`/api/events`) rather than surfaced by `transport/a2a`. Behaviourally covered by
  `ui > TestSSEReceivesEvent`; there is no test at the supervisor/A2A layer. Marked PARTIAL.
- **W-4** — `agent-supervisor` scenario "Two agents in one company never share a queue" is only
  partially proven. `TestStore_TenantIsolation` uses the same agent name across two tenants, and
  `TestMaterialize_TwoAgentStartsTwoGoroutines` asserts distinct endpoints and IDLE state but not
  queue non-crossover. No test uses two distinct agent names in the same tenant and asserts that
  a task submitted to one does not appear in the other's queue. The mechanism (address-keyed
  store) makes this very likely correct but it is unasserted.
- **W-5** — Architectural deviation: `port.BoundedContext` is assembled and then flattened to a
  plain string by `contextText()` before crossing the provider boundary, so the typed bounded
  context contract declared in `core/port` never actually crosses the seam.
- **W-6** — Strict TDD compliance is unverifiable: no `apply-progress` artifact exists for this
  change, so there is no TDD Cycle Evidence table to cross-reference. Per orchestrator
  instruction this absence is not itself a failure.
- **W-7** — `cmd/company` coverage is 41.7% and `core/supervisor` is 77.9%, both below an 80%
  guideline. `cmd/company` holds the wiring and E2E entry points, which are the least-covered
  paths in the system.
- **W-8** — Artifact-store discrepancy: `gentle-ai sdd-status` reports
  `artifactStore: openspec`, while `openspec/config.yaml` declares `artifact_store: both` and
  the session declares `hybrid`. The session-declared value was used (file + Engram). Worth
  reconciling so native status and config agree.

**SUGGESTION**

- **S-1** — `port.ResumePoint` (`core/port/provider.go:120`) is dead code: declared but never
  referenced by any production code. Either wire it into the resume path or remove it.
- **S-2** — Remove the stale `ProviderCapabilities` doc comment at `core/port/provider.go:101`
  regardless of how C-1 is resolved; it currently documents a contract that does not exist.
- **S-3** — `gateways/telegram > TestIntegration_LiveSend` is the only skipped test and requires
  `TELEGRAM_BOT_TOKEN` / `TELEGRAM_OWNER_ID`. Consider documenting how to run it in CI with
  secrets so the outbound path gets periodic live coverage.
- **S-4** — The working tree carries staged archive renames for an unrelated change
  (`validate-model-startup`) plus two untracked canonical spec directories
  (`openspec/specs/adapter-stderr-capture/`, `openspec/specs/model-startup-validation/`). Not
  part of this change; flagged so it is not accidentally bundled.

### Verdict

**FAIL** — Every declared command passes (`go build`, `go vet`, `go test -race` all exit `0`, no
races, 145/145 tests green), but two spec requirements are not satisfied: "Providers Declare
Their Capabilities" is entirely unimplemented and untested, and "The UI Cannot Pause, Kill,
Reassign, or Command an Agent" is contradicted by `POST /api/send`, which the test suite asserts
as correct behaviour.
