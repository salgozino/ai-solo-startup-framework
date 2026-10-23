# Delta for Agent Supervisor (Client Follow-Up)

## ADDED Requirements

### Requirement: A Follow-Up Continues a Terminal Task as a New Linked Task

A new task whose message carries `ReferenceTasks` naming a task of the same supervisor MUST be
executed as a follow-up of that task. The referenced task MUST be in a terminal state
(`COMPLETED`, `FAILED`, `REJECTED` or `CANCELED`); otherwise, or when the supervisor has no record of it, the
follow-up MUST fail naming the referenced task and its problem. The follow-up MUST persist the
referenced task id as `follow_up_of` and MUST leave the referenced task's record unchanged.

#### Scenario: A follow-up of a completed task inherits the whole chain

- GIVEN task T1 completed with output "draft v1", and task T2 is a follow-up of T1 that completed
- WHEN the client sends "make it shorter" as a follow-up of T2
- THEN a new task T3 is created with `follow_up_of = T2`
- AND the first provider input of T3 contains T1's input, "draft v1", T2's transcript and outcome,
  and "make it shorter", oldest first
- AND T1 and T2 keep their state and output

#### Scenario: A follow-up of a non-terminal task fails

- GIVEN task T1 is `WORKING`
- WHEN a follow-up of T1 is executed
- THEN the follow-up fails with an error naming T1 and stating it is not terminal
- AND no provider is invoked

### Requirement: A Follow-Up Has Its Own Round Budget

The provider round cap MUST count only the assistant turns produced by the follow-up itself,
never the turns it inherited.

#### Scenario: Inherited rounds do not exhaust the cap

- GIVEN `max_rounds: 2` and a previous task that used 2 rounds
- WHEN its follow-up delegates once and then finishes
- THEN the follow-up completes
