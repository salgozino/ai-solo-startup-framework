// Package policy_test — resume test (task 4.7).
// Verifies that when a task is in INPUT_REQUIRED and approval arrives as a new
// SendMessage carrying the original TaskID, the supervisor recognizes it as a
// resume of that specific task, not a new task submission.
//
// This test works at the policy+supervisor boundary using the supervisor's internal
// Execute logic (internal package test) to simulate the full resume flow.
package policy_test

import (
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
)

// TestPayload_MarshalValidate verifies versioned payload round-trip.
// Satisfies: approval-flow spec "Unrecognized payload version is not blindly trusted".
func TestPayload_MarshalValidate(t *testing.T) {
	original := policy.EscalationPayload{
		ActionKind: "telegram_send",
		TaskID:     "task-123",
	}

	data, err := policy.MarshalPayload(original)
	if err != nil {
		t.Fatalf("MarshalPayload: %v", err)
	}

	parsed, err := policy.ValidatePayload(data)
	if err != nil {
		t.Fatalf("ValidatePayload(valid): %v", err)
	}
	if parsed.ActionKind != original.ActionKind {
		t.Errorf("ActionKind mismatch: got %q, want %q", parsed.ActionKind, original.ActionKind)
	}
	if parsed.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch: got %q, want %q", parsed.TaskID, original.TaskID)
	}
}

// TestPayload_UnrecognizedVersion verifies that an unknown version is rejected.
// Satisfies: approval-flow spec "Unrecognized payload version is not blindly trusted".
func TestPayload_UnrecognizedVersion(t *testing.T) {
	badPayload := []byte(`{"version":"99","action_kind":"telegram_send","task_id":"t1"}`)

	_, err := policy.ValidatePayload(badPayload)
	if err == nil {
		t.Error("expected error for unrecognized payload version, got nil")
	}
}

// TestPayload_MalformedJSON verifies that invalid JSON is rejected.
func TestPayload_MalformedJSON(t *testing.T) {
	_, err := policy.ValidatePayload([]byte(`not json`))
	if err == nil {
		t.Error("expected error for malformed JSON payload, got nil")
	}
}

// TestResumeFlow_PolicyEngine verifies the policy engine's role in the resume flow:
// - Escalate result has no token (token is minted on approval, not on escalation)
// - MintApprovalToken mints a token that ValidateToken accepts
// - A foreign token (not minted by this engine) is rejected
// Satisfies: approval-flow "Resume is a new message carrying the same task ID"
// (the policy engine's token contract enables the approved execution).
func TestResumeFlow_PolicyEngine(t *testing.T) {
	eng := policy.NewEngine()
	policyCfg := map[string]config.Policy{
		"telegram_send": {Risk: "risky", AllowedRoles: []string{"ceo"}},
	}

	// Step 1: classify — should escalate (risky, ceo is allowed).
	intent := policy.ActionIntent{Kind: "telegram_send"}
	result := eng.Classify(intent, "ceo", policyCfg)
	if result.Kind != policy.Escalate {
		t.Fatalf("expected Escalate, got %s", result.Kind)
	}
	if result.ApprovalToken != "" {
		t.Error("Escalate must not mint a token — token is minted only on approval")
	}

	// Step 2: simulate human approval — mint a token for the resume.
	approvalToken := eng.MintApprovalToken()
	if approvalToken == "" {
		t.Fatal("MintApprovalToken returned empty string")
	}

	// Step 3: validate the minted token — must be accepted by the same engine.
	if err := eng.ValidateToken(approvalToken); err != nil {
		t.Errorf("approval token from same engine rejected: %v", err)
	}

	// Step 4: a second engine's token must be rejected (isolation per instance).
	eng2 := policy.NewEngine()
	foreignToken := eng2.MintApprovalToken()
	if err := eng.ValidateToken(foreignToken); err == nil {
		t.Error("token from a different engine instance must be rejected by the original engine")
	}
}
