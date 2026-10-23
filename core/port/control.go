package port

// KindFinishTask is the name of the supervisor control tool the model calls to end its
// task. It is NOT an action kind: it is never declared in risk_policy, never classified
// by the policy engine, and never routed to a gateway or the delegator. It travels to the
// supervisor as an ActionIntent with Payload["body"] holding the final answer text, and
// the supervisor strips it from the intents before classification.
const KindFinishTask = "finish_task"
