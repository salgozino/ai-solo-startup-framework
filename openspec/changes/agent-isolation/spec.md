# Spec: Agent Isolation via --bare and System Prompt Files

## Capability: agent-isolation

### Purpose

Ensure all spawned agents run in isolated mode, independent of the user's local environment and configuration.

### Requirements

#### Requirement: Unconditional Isolation — Claude Code

All agents spawned via the Claude Code adapter MUST include `--bare` and `--no-session-persistence` in every invocation, regardless of any other configuration.

##### Scenario: Claude Code agent invoked without system_prompt

- GIVEN a Claude Code agent configured with no `system_prompt` field
- WHEN the adapter builds its argument list
- THEN the invocation MUST contain `--bare` and `--no-session-persistence`
- AND MUST NOT contain `--system-prompt-file`

##### Scenario: Claude Code agent invoked with system_prompt

- GIVEN a Claude Code agent with a valid resolved `system_prompt` absolute path
- WHEN the adapter builds its argument list
- THEN the invocation MUST contain `--bare`, `--no-session-persistence`, and `--system-prompt-file <absolute-path>`

#### Requirement: Unconditional Isolation — OpenCode

All agents spawned via the OpenCode adapter MUST include `--pure` in every invocation.

##### Scenario: OpenCode agent invoked without system_prompt

- GIVEN an OpenCode agent with no `system_prompt` field
- WHEN the adapter builds its argument list
- THEN the invocation MUST contain `--pure`
- AND MUST NOT prepend any system content to the task input

##### Scenario: OpenCode agent invoked with system_prompt

- GIVEN an OpenCode agent with a valid resolved `system_prompt` absolute path
- WHEN the adapter builds its argument list
- THEN the invocation MUST contain `--pure`
- AND MUST prepend `[SYSTEM]\n{file-content}\n\n` to the task input before delivery

---

## Capability: system-prompt-config

### Purpose

Allow per-agent persona files to be declared in `company.yaml` and applied at agent invocation time to enforce declared roles.

### Requirements

#### Requirement: Optional system_prompt Field

`AgentConfig` MAY include a `system_prompt` field with a relative file path. When absent, agents run with isolation flags only and MUST start without error.

##### Scenario: system_prompt declared with valid path

- GIVEN a `company.yaml` agent entry with `system_prompt: agents/advisor.md`
- AND the file exists relative to `company.yaml`
- WHEN the config is loaded
- THEN `AgentConfig.SystemPrompt` MUST hold the resolved absolute path
- AND no load error MUST be returned

##### Scenario: system_prompt absent

- GIVEN a `company.yaml` agent entry with no `system_prompt` field
- WHEN the config is loaded
- THEN `AgentConfig.SystemPrompt` MUST be empty string
- AND the agent MUST start without error

#### Requirement: File Existence Validated at Load Time

When `system_prompt` is set, the referenced file MUST exist at config load time. The system MUST fail with a descriptive error identifying the missing path before any agent starts.

##### Scenario: Referenced file missing at load

- GIVEN `system_prompt: agents/missing.md` in `company.yaml`
- AND the file does not exist on disk
- WHEN the config is loaded
- THEN loading MUST fail with an error that names the missing file path
- AND no agent MAY be started

##### Scenario: Unknown field in agent config entry

- GIVEN a `company.yaml` agent entry with an unrecognized field (e.g. `foo: bar`)
- WHEN the config is loaded
- THEN loading MUST fail with a field-validation error

#### Requirement: Path Resolved Relative to company.yaml

The `system_prompt` value MUST be resolved relative to the directory of `company.yaml`. The adapter MUST receive the resolved absolute path, never the raw relative value.

##### Scenario: Relative path resolved correctly

- GIVEN `company.yaml` at `/project/company.yaml` and `system_prompt: agents/cto.md`
- WHEN wire materializes the agent
- THEN the adapter MUST receive `/project/agents/cto.md` as the system prompt path

##### Scenario: Absolute path in system_prompt field

- GIVEN `system_prompt` is an absolute path (e.g. `/etc/prompts/cto.md`)
- WHEN the config is loaded
- THEN the path MUST be used as-is without modification

#### Requirement: README Documents Isolation and system_prompt

The `README.md` MUST document: isolation flags per adapter, the `system_prompt` field format, the `agents/` folder convention, and MUST NOT present curl as a primary interaction method.

##### Scenario: User consults README for system_prompt

- GIVEN the updated README
- WHEN a user searches for `system_prompt` or isolation
- THEN they MUST find documentation explaining the field, its path format, and per-adapter behavior (--system-prompt-file vs content-prepend)
