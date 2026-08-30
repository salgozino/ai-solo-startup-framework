// Package policy implements the two-stage risk classification engine.
// Stage 1: capability check (allowed_roles).
// Stage 2: risk check (risky → escalate, else permit).
// Tokens are minted only by this engine; the gateway requires one to send.
package policy

import "github.com/salgozino/ai-solo-startup-framework/config"

// ResultKind is the outcome of classifying an action intent.
type ResultKind string

const (
	// HardDeny: role not in allowed_roles → task REJECTED, no escalation, no token.
	HardDeny ResultKind = "HardDeny"
	// Escalate: role allowed, action risky → task INPUT_REQUIRED, no token yet.
	Escalate ResultKind = "Escalate"
	// Permit: role allowed, action not risky → execute directly, token minted.
	Permit ResultKind = "Permit"
)

// ActionIntent is the intent emitted by a provider that must be classified.
type ActionIntent struct {
	// Kind identifies the action (e.g. "telegram_send").
	Kind string
}

// ClassificationResult holds the outcome of Engine.Classify.
type ClassificationResult struct {
	// Kind is the classification outcome.
	Kind ResultKind
	// ApprovalToken is non-empty only when Kind == Permit.
	// It is never minted for HardDeny or unapproved Escalate.
	ApprovalToken string
	// Escalate mirrors Kind == Escalate for convenient boolean checks.
	Escalate bool
}

// Engine classifies action intents and mints approval tokens.
type Engine struct {
	store *tokenStore
}

// NewEngine constructs an Engine with its own token store.
// Tokens minted by this instance are valid only within this instance.
func NewEngine() *Engine {
	return &Engine{store: newTokenStore()}
}

// Classify runs the two-stage classification for intent emitted by an agent with role.
//
//   Stage 1 (capability): is role in policy[intent.Kind].AllowedRoles?
//   If NO → HardDeny (terminal REJECTED, no escalation offered).
//
//   Stage 2 (risk): if role is allowed, is the action risky?
//   If YES → Escalate (INPUT_REQUIRED, no token until human approves).
//   If NO  → Permit (mint a token, execute directly).
func (e *Engine) Classify(intent ActionIntent, role string, policies map[string]config.Policy) ClassificationResult {
	p, ok := policies[intent.Kind]
	if !ok {
		// Unknown action kind — hard deny by default (fail closed).
		return ClassificationResult{Kind: HardDeny}
	}

	// Stage 1: capability check.
	if !contains(p.AllowedRoles, role) {
		return ClassificationResult{Kind: HardDeny}
	}

	// Stage 2: risk check.
	if p.Risk == "risky" {
		return ClassificationResult{Kind: Escalate, Escalate: true}
	}

	// Permit: mint a token the gateway will require.
	token := e.store.mint()
	return ClassificationResult{Kind: Permit, ApprovalToken: token}
}

// MintApprovalToken mints a token for a human-approved escalation.
// Call this after the human approves an Escalate result.
func (e *Engine) MintApprovalToken() string {
	return e.store.mint()
}

// ValidateToken returns nil if token was minted by this engine instance.
// Returns an error for any token not issued by this engine.
func (e *Engine) ValidateToken(token string) error {
	return e.store.validate(token)
}

// contains reports whether s is in the slice.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
