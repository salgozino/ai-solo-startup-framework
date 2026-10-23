package mcp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// bodyArg is the single argument key every action tool accepts. It is not cosmetic:
// core/supervisor's extractBody reads only ActionIntent.Payload["body"], so an intent
// recorded under any other key reaches the gateway with empty text.
const bodyArg = "body"

// finishRejectedText is the error a finish_task call gets after a delegate_task intent
// was recorded in the same invocation; the supervisor backstops the same rule.
const finishRejectedText = "finish_task rejected: this turn delegated work. The peer's result arrives in your next turn; call finish_task then."

// buildToolDescription returns the spec-mandated disclosure text for a tool of the given kind.
// Design Decision C: description must state it records intent and does not execute.
// It also names the body argument, so the contract is legible to the model in prose as
// well as in the input schema. delegate_task additionally names the required target
// argument (design D11): the model must know the delegation needs an addressed role, not
// only a message body.
func buildToolDescription(kind string) string {
	desc := fmt.Sprintf(
		"Calling this tool records an intent for %s for later policy classification and does not execute the action. "+
			"Pass the full message text to be delivered in the required %q argument; it is used verbatim if the intent is approved. "+
			"Call once per request; repeating an identical call within a turn is ignored.",
		kind, bodyArg,
	)
	if kind == port.KindFinishTask {
		return fmt.Sprintf(
			"Ends the task. Pass your final answer to the user in the required %q argument; it becomes the task's result. "+
				"Call it only when no further work or delegation results are needed. The task ends after this turn. "+
				"A finish_task in a turn that delegates work is rejected: the peer's result arrives in your next turn, so "+
				"call finish_task then. If you do not call it, a turn that delegates is continued with "+
				"the delegation results, and a turn that does not delegate ends with your reply.",
			bodyArg)
	}
	if kind == port.KindDelegateTask {
		desc += fmt.Sprintf(
			" The required %q argument identifies the delegation's target role (e.g. \"engineer\"); it must be a role declared in company.yaml, never a specific agent's configured name.",
			port.TargetArg,
		)
	}
	return desc
}

// targetSchema returns the input schema for the "target" argument, required only for
// the delegate_task action kind (design D11).
func targetSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Description: "Identifies the delegation's target role (e.g. \"engineer\"). Must be a role declared in company.yaml, never a specific agent's configured name.",
	}
}

// buildInputSchema returns the input schema advertised for an action tool.
//
// Without an explicit schema, gomcp.AddTool infers one from the In type parameter
// (map[string]any), producing an unconstrained object with no properties and no required
// keys — which never tells the agent which argument key to use. The schema is built fresh
// per tool so no two registered tools share a *jsonschema.Schema pointer.
//
// Additional properties are deliberately left permitted: the handler records the whole
// argument map into ActionIntent.Payload, and only "body" (and, for delegate_task,
// "target") is load-bearing downstream.
//
// delegate_task is the one action kind whose schema requires a second argument (design
// D11): comparing against the exported port.KindDelegateTask constant, not a naming
// convention, so a rename of that constant is a compile error here, not silent drift.
func buildInputSchema(kind string) *jsonschema.Schema {
	s := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			bodyArg: {
				Type: "string",
				Description: fmt.Sprintf(
					"The message text to be delivered by %s if the intent is approved. Sent verbatim; must not be empty.",
					kind,
				),
			},
		},
		Required: []string{bodyArg},
	}
	if kind == port.KindDelegateTask {
		s.Properties[port.TargetArg] = targetSchema()
		s.Required = append(s.Required, port.TargetArg)
	}
	return s
}

// dedupeKey builds a deterministic key from token + kind + a sorted-key JSON encoding of payload.
// Identical (token, kind, payload) triples produce the same key, enabling repeat-call detection.
func dedupeKey(token, kind string, payload map[string]any) string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	sorted := make(map[string]any, len(payload))
	for _, k := range keys {
		sorted[k] = payload[k]
	}
	b, _ := json.Marshal(sorted)
	return token + "\x00" + kind + "\x00" + string(b)
}

// buildAckResult returns the acknowledge-without-execute CallToolResult.
// isError is always false (errors trigger model retry; we want acknowledgment).
// Design Decision C text format and StructuredContent layout.
func buildAckResult(receipt, kind string, duplicate bool) *gomcp.CallToolResult {
	var msg string
	switch kind {
	case port.KindFinishTask:
		msg = fmt.Sprintf("Recorded %s as %s. The task ends after this turn; your answer is the final result.", kind, receipt)
	case port.KindDelegateTask:
		msg = fmt.Sprintf(
			"Recorded intent %s as %s. Pending classification. If permitted, the peer's result arrives in your next turn's transcript; do not call again for the same request this turn. Until then, report it as requested, not completed.",
			kind, receipt)
	default:
		msg = fmt.Sprintf(
			"Recorded intent %s as %s. Pending classification; this action does not return a result to you. Do not call again for the same request. Report it as requested, not completed.",
			kind, receipt)
	}
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: msg},
		},
		StructuredContent: map[string]any{
			"receipt":   receipt,
			"kind":      kind,
			"status":    "recorded",
			"duplicate": duplicate,
		},
	}
}

// newReceipt generates a random 16-byte base64url receipt ID.
func newReceipt() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("mcp: tools: crypto/rand.Read: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// registerTools adds one MCP tool per policy key to srv.
// Each tool handler:
//  1. Resolves the bearer token from TokenInfo.UserID to an invocation.
//  2. Checks dedupe (returns existing receipt if this is a repeat call).
//  3. Records the ActionIntent in the invocation sink.
//  4. Returns the acknowledge-without-execute result.
//
// Invariant: every *action* tool is policy-classified; supervisor control tools are a
// fixed, named set (today only finish_task). Control tools are registered here but sit
// outside risk_policy: no company.yaml declaration is needed, and the supervisor strips
// them from the intents before classification, so they never reach the policy engine, a
// gateway or the delegator. A policy key that collides with a control tool name is ignored.
func registerTools(srv *gomcp.Server, tenant string, policies map[string]config.Policy, registry *Registry) {
	kinds := []string{port.KindFinishTask}
	for kind := range policies {
		if kind != port.KindFinishTask {
			kinds = append(kinds, kind)
		}
	}
	for _, kind := range kinds {
		kind := kind // capture loop variable

		gomcp.AddTool(srv,
			&gomcp.Tool{
				Name:        kind,
				Description: buildToolDescription(kind),
				InputSchema: buildInputSchema(kind),
			},
			func(ctx context.Context, req *gomcp.CallToolRequest, args map[string]any) (*gomcp.CallToolResult, any, error) {
				if req.Extra == nil || req.Extra.TokenInfo == nil {
					return &gomcp.CallToolResult{
						IsError: true,
						Content: []gomcp.Content{&gomcp.TextContent{Text: "no auth token present"}},
					}, nil, nil
				}

				token := req.Extra.TokenInfo.UserID
				inv, err := registry.Resolve(token, tenant)
				if err != nil {
					// Tenant mismatch or other resolve failure.
					return &gomcp.CallToolResult{
						IsError: true,
						Content: []gomcp.Content{&gomcp.TextContent{Text: "unauthorized: " + err.Error()}},
					}, nil, nil
				}

				dkey := dedupeKey(token, kind, args)

				// Lock spans check+Record+insert so a failed Record can't leave a false dedupe entry.
				inv.mu.Lock()
				defer inv.mu.Unlock()

				if existingReceipt, isDupe := inv.dedupe[dkey]; isDupe {
					return buildAckResult(existingReceipt, kind, true), nil, nil
				}

				if kind == port.KindFinishTask && inv.delegated {
					return &gomcp.CallToolResult{
						IsError: true,
						Content: []gomcp.Content{&gomcp.TextContent{Text: finishRejectedText}},
					}, nil, nil
				}

				intent := port.ActionIntent{
					Kind:    kind,
					Payload: args,
				}
				if err := inv.sink.Record(intent); err != nil {
					return &gomcp.CallToolResult{
						IsError: true,
						Content: []gomcp.Content{&gomcp.TextContent{Text: "intent sink full: " + err.Error()}},
					}, nil, nil
				}

				if kind == port.KindDelegateTask {
					inv.delegated = true
				}
				receipt := newReceipt()
				inv.dedupe[dkey] = receipt
				return buildAckResult(receipt, kind, false), nil, nil
			},
		)
	}
}
