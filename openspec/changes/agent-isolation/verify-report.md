```yaml
change: agent-isolation
mode: strict-tdd
verdict: FAIL
timestamp: "2026-09-08T13:30:00-03:00"

test_command: "go test ./... -count=1"
test_exit_code: 0
test_output_hash: "e8c46d334e726795ceff0d6505f877eb6b5f0f5c265a2ff3b3b4eca6a26e6d78"
vet_command: "go vet ./..."
vet_exit_code: 0

# ── Completeness ──────────────────────────────────────────────────────────────
completeness:
  artifacts:
    proposal: present
    spec: present
    design: present
    tasks: present
    apply_progress: present
  tasks_total: 14
  tasks_complete: 14
  tasks_incomplete: 0

# ── Build / Tests / Coverage ─────────────────────────────────────────────────
build:
  status: PASS
  vet: clean

tests:
  status: PASS
  packages_tested: 11
  packages_skipped: 1  # core/port/fake — no test files
  all_passed: true

coverage:
  adapters/claudecode: "84.6%"
  adapters/opencode: "86.3%"
  cmd/company: "42.7%"
  config: "95.1%"
  core/address: "86.7%"
  core/policy: "95.0%"
  core/port: "100.0%"
  core/supervisor: "77.9%"
  gateways/telegram: "94.3%"
  transport/a2a: "85.5%"
  ui: "82.9%"

# ── Spec Compliance Matrix ───────────────────────────────────────────────────
# Spec heading levels: #### Requirement / ##### Scenario (not ### / ####).
# Native ### counter returns 0/0 — actual hand-count: 6 requirements, 11 scenarios.
# Mismatch noted; envelope uses actual hand-count.
requirements_total: 6
scenarios_total: 11

compliance:
  - id: R1
    name: "Unconditional Isolation — Claude Code"
    status: PASS
    scenarios:
      - id: S1
        name: "Claude Code agent invoked without system_prompt"
        status: PASS
        covering_tests:
          - "claudecode: TestBareFlag_AlwaysPresent"
          - "claudecode: TestSystemPromptFile_WhenNotSet"
        evidence: "fakeclaude echoes 'bare:1|' prefix; test asserts --bare present, --system-prompt-file absent"

      - id: S2
        name: "Claude Code agent invoked with system_prompt"
        status: PASS
        covering_tests:
          - "claudecode: TestSystemPromptFile_WhenSet"
        evidence: "adapter.go:74-80 always includes --bare --no-session-persistence; appends --system-prompt-file only when systemPromptPath != ''"

  - id: R2
    name: "Unconditional Isolation — OpenCode"
    status: PASS
    scenarios:
      - id: S3
        name: "OpenCode agent invoked without system_prompt"
        status: PASS
        covering_tests:
          - "opencode: TestPureFlag_AlwaysPresent"
          - "opencode: TestContentPrepend_WhenNotSet"
        evidence: "fakeopencode echoes 'pure:1|' prefix; test asserts --pure present, no [SYSTEM] prepend"

      - id: S4
        name: "OpenCode agent invoked with system_prompt"
        status: PASS
        covering_tests:
          - "opencode: TestContentPrepend_WhenSet"
        evidence: "adapter.go:87-97 always includes --pure; prepends '[SYSTEM]\n{content}\n\n' when content != ''"

  - id: R3
    name: "Optional system_prompt Field"
    status: PASS
    scenarios:
      - id: S5
        name: "system_prompt declared with valid path"
        status: PASS
        covering_tests:
          - "config: TestLoad/valid_system_prompt_resolves_to_absolute_path"
        evidence: "checks AgentConfig.SystemPrompt is non-empty, absolute, and ends with testdata/agents/ceo.md"

      - id: S6
        name: "system_prompt absent"
        status: PASS
        covering_tests:
          - "config: TestLoad/absent_system_prompt_field_yields_empty_string"
        evidence: "uses testdata/valid.yaml (no system_prompt); asserts AgentConfig.SystemPrompt == ''"

  - id: R4
    name: "File Existence Validated at Load Time"
    status: PASS
    scenarios:
      - id: S7
        name: "Referenced file missing at load"
        status: PASS
        covering_tests:
          - "config: TestLoad/system_prompt_referencing_missing_file_is_rejected"
        evidence: "errContains: 'agents/missing.md'; schema.go:130-132 calls os.Stat and returns descriptive error naming the abs path"

      - id: S8
        name: "Unknown field in agent config entry"
        status: PASS
        note: "Covered by agent-level_gateway_field_is_rejected (gateways at agent level = unknown field). Generic unknown-field path (schema.go default case) not separately tested but whitelist switch covers all unknowns identically."
        covering_tests:
          - "config: TestLoad/agent-level_gateway_field_is_rejected"
        evidence: "schema.go:106-112 switch default returns 'unknown field %q'; gateways-specific check at :101 is a prior guard for clearer errors"

  - id: R5
    name: "Path Resolved Relative to company.yaml"
    status: FAIL
    scenarios:
      - id: S9
        name: "Relative path resolved correctly"
        status: PASS
        covering_tests:
          - "config: TestLoad/valid_system_prompt_resolves_to_absolute_path"
        evidence: "asserts filepath.IsAbs(got) && strings.HasSuffix(got, 'testdata/agents/ceo.md'); schema.go:123-124 joins basedir+rawPrompt when not absolute"

      - id: S10
        name: "Absolute path in system_prompt field"
        status: CRITICAL
        verdict: UNTESTED
        covering_tests: []
        evidence: |
          schema.go:123 contains `if !filepath.IsAbs(rawPrompt)` which correctly skips the Join
          for absolute paths, letting filepath.Abs() return it unchanged. The code is correct,
          but NO test fixture or test case passes an absolute system_prompt value. Under
          Strict TDD, a spec scenario with no passing covering runtime test is CRITICAL.

  - id: R6
    name: "README Documents Isolation and system_prompt"
    status: PASS
    note: "Documentation scenario — no runtime test applicable. Verified by source inspection."
    scenarios:
      - id: S11
        name: "User consults README for system_prompt"
        status: PASS
        covering_tests: []
        evidence: |
          README.md confirmed to contain:
          - 'system_prompt: agents/ceo.md' (line 31)
          - 'Agent isolation' section with isolation flag table (lines 66-73)
          - 'System prompts and the agents/ folder' section (lines 77-103)
          - Per-adapter behavior: '--system-prompt-file' vs 'content-prepend' (lines 91-94)
          - No curl as primary interaction method (UI described as primary)

# ── Design Coherence ─────────────────────────────────────────────────────────
design_coherence:
  status: PASS
  findings:
    - "claudecode.New() signature matches design: (bin, opts, model, systemPromptPath string)"
    - "opencode.New() signature matches design: (bin, opts, model, agentName, systemPromptPath string)"
    - "wire.go passes agCfg.SystemPrompt to both claudecode.New and opencode.New — matches design"
    - "File read at construction time in opencode (os.ReadFile at New()) — matches design"
    - "Path validated at config.Load() time — matches design"
    - "basedir derived from filepath.Dir(path) — matches design"

# ── Issues ────────────────────────────────────────────────────────────────────
issues:
  critical:
    - id: C1
      scenario: S10
      requirement: R5
      description: >
        Scenario "Absolute path in system_prompt field" has no covering runtime test.
        schema.go:123 (`if !filepath.IsAbs(rawPrompt)`) correctly handles the absolute
        path case, but no test fixture exercises this code path. Strict TDD requires
        a passing runtime test for every spec scenario.
      fix: >
        Add a test case to config/schema_test.go that creates a temp file,
        passes its absolute path as system_prompt in a test YAML fixture,
        and asserts the returned AgentConfig.SystemPrompt equals that absolute path
        unchanged. Example errContains check unnecessary — wantErr: false with
        check func asserting got == absolutePath.

  warnings:
    - id: W1
      description: >
        Implementation changes are uncommitted on master (unstaged working-tree changes).
        No branch or PR has been created. All spec work lives only on disk.
      fix: Create a feature branch, commit, and open a PR.

    - id: W2
      description: >
        cmd/company coverage is 42.7% — lowest among tested packages. The composition
        root (wire.go) is difficult to integration-test without real binaries, but the
        gap is significant. The existing cmd/company tests cover happy-path wiring only.
      fix: Consider adding error-path tests for unknown provider, address creation failure,
           or store creation failure. Not a spec violation; informational.

    - id: W3
      description: >
        Spec heading levels use '####'/'#####' instead of '###'/'####'. The native
        '### Requirement:' counter returns 0; actual hand-count: 6 requirements,
        11 scenarios. This is a spec authoring format issue, not an implementation defect.
      fix: Align spec heading levels with the native counter convention in the next
           spec revision (### Requirement / #### Scenario).

# ── Final Verdict ─────────────────────────────────────────────────────────────
summary:
  requirements_pass: 5
  requirements_fail: 1   # R5 (one scenario untested)
  scenarios_pass: 10
  scenarios_fail: 1      # S10 — CRITICAL UNTESTED
  critical_count: 1
  warning_count: 3

verdict: FAIL
verdict_reason: >
  One spec scenario (S10: Absolute path in system_prompt field) has no covering runtime
  test under Strict TDD mode. All other 10 scenarios pass with direct runtime evidence.
  Implementation is functionally correct for all 6 requirements. Adding a single test case
  to config/schema_test.go resolves the CRITICAL finding and enables a PASS verdict.
```
