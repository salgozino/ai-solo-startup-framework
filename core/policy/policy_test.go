// Package policy_test verifies the two-stage classification engine.
// Task 4.1 RED: these tests are written before any production code.
// Threat-matrix gateway-effect cases (b) and (e).
package policy_test

import (
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
)

func makePolicy(risk string, allowedRoles []string) map[string]config.Policy {
	return map[string]config.Policy{
		"telegram_send": {
			Risk:         risk,
			AllowedRoles: allowedRoles,
		},
	}
}

// TestClassify_DisallowedRole verifies hard-deny: REJECTED, no token, no escalation.
// Threat-matrix gateway-effect case (e).
func TestClassify_DisallowedRole(t *testing.T) {
	eng := policy.NewEngine()
	intent := policy.ActionIntent{Kind: "telegram_send"}
	cfg := makePolicy("risky", []string{"ceo"})

	result := eng.Classify(intent, "engineer", cfg)

	if result.Kind != policy.HardDeny {
		t.Errorf("expected HardDeny, got %s", result.Kind)
	}
	if result.ApprovalToken != "" {
		t.Error("HardDeny must mint no token")
	}
	if result.Escalate {
		t.Error("HardDeny must not trigger escalation")
	}
}

// TestClassify_AllowedRiskyRole verifies escalation path: INPUT_REQUIRED, no token yet.
// Threat-matrix gateway-effect case (b) setup — escalate first, approve later.
func TestClassify_AllowedRiskyRole(t *testing.T) {
	eng := policy.NewEngine()
	intent := policy.ActionIntent{Kind: "telegram_send"}
	cfg := makePolicy("risky", []string{"ceo"})

	result := eng.Classify(intent, "ceo", cfg)

	if result.Kind != policy.Escalate {
		t.Errorf("expected Escalate, got %s", result.Kind)
	}
	if result.ApprovalToken != "" {
		t.Error("Escalate must not mint a token before approval")
	}
	if !result.Escalate {
		t.Error("Escalate result must have Escalate=true")
	}
}

// TestClassify_AllowedNonRiskyRole verifies direct execution: Permit with a token.
func TestClassify_AllowedNonRiskyRole(t *testing.T) {
	eng := policy.NewEngine()
	intent := policy.ActionIntent{Kind: "telegram_send"}
	cfg := makePolicy("safe", []string{"ceo"})

	result := eng.Classify(intent, "ceo", cfg)

	if result.Kind != policy.Permit {
		t.Errorf("expected Permit, got %s", result.Kind)
	}
	if result.ApprovalToken == "" {
		t.Error("Permit must mint an approval token")
	}
	if result.Escalate {
		t.Error("Permit must not escalate")
	}
}

// TestClassify_HardDenyIsNotFailed verifies REJECTED is distinct from FAILED.
// REJECTED = policy hard-deny (terminal, no send).
// FAILED   = provider/gateway error (different terminal state).
func TestClassify_HardDenyIsNotFailed(t *testing.T) {
	eng := policy.NewEngine()
	intent := policy.ActionIntent{Kind: "telegram_send"}
	cfg := makePolicy("risky", []string{"ceo"})

	result := eng.Classify(intent, "engineer", cfg)

	// HardDeny produces REJECTED-semantics: terminal, no send, no escalation.
	// It must NOT be the Permit or Escalate kind (which could lead to a send).
	if result.Kind == policy.Permit {
		t.Error("HardDeny must not be Permit")
	}
	if result.Kind == policy.Escalate {
		t.Error("HardDeny must not be Escalate (no escalation offered on hard deny)")
	}
	if result.Kind != policy.HardDeny {
		t.Errorf("hard-denied intent must return HardDeny, not %s", result.Kind)
	}
}

// TestValidateToken_OnlyEngineMintsAccepted verifies Validate rejects foreign tokens.
func TestValidateToken_OnlyEngineMintsAccepted(t *testing.T) {
	eng := policy.NewEngine()
	intent := policy.ActionIntent{Kind: "telegram_send"}
	cfg := makePolicy("safe", []string{"ceo"})

	result := eng.Classify(intent, "ceo", cfg)
	if result.ApprovalToken == "" {
		t.Fatal("expected a minted token for Permit")
	}

	// Token from this engine must be valid.
	if err := eng.ValidateToken(result.ApprovalToken); err != nil {
		t.Errorf("own-minted token rejected: %v", err)
	}

	// Foreign token must be rejected.
	if err := eng.ValidateToken("not-a-real-token"); err == nil {
		t.Error("foreign token must be rejected")
	}
}

// TestMintOnApproval verifies that MintApprovalToken produces a valid token.
func TestMintOnApproval(t *testing.T) {
	eng := policy.NewEngine()

	token := eng.MintApprovalToken()
	if token == "" {
		t.Fatal("MintApprovalToken returned empty string")
	}
	if err := eng.ValidateToken(token); err != nil {
		t.Errorf("freshly minted token rejected: %v", err)
	}
}
