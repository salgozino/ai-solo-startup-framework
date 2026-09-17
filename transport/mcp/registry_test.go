// Package mcp tests for the Registry type.
package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

func TestRegistry_MintResolveAndDrain(t *testing.T) {
	reg := NewRegistry()
	exp := time.Now().Add(time.Hour)

	token, handle := reg.Mint("acme", "worker", "t1", exp)

	if token == "" {
		t.Fatal("Mint: expected non-empty token")
	}

	// Resolve — should succeed and return the registered identity.
	inv, err := reg.Resolve(token, "acme")
	if err != nil {
		t.Fatalf("Resolve after Mint: unexpected error: %v", err)
	}
	if inv.tenant != "acme" || inv.agent != "worker" || inv.taskID != "t1" {
		t.Errorf("Resolve: identity mismatch: got tenant=%q agent=%q taskID=%q", inv.tenant, inv.agent, inv.taskID)
	}

	// Drain — empty sink initially, entry deleted.
	intents := handle.Drain()
	if len(intents) != 0 {
		t.Errorf("Drain on empty sink: expected 0 intents, got %d", len(intents))
	}

	// Resolve after Drain — entry deleted, must return error.
	_, err = reg.Resolve(token, "acme")
	if err == nil {
		t.Error("Resolve after Drain: expected error (entry deleted), got nil")
	}
}

func TestRegistry_MintResolveAndDrain_WithIntents(t *testing.T) {
	reg := NewRegistry()
	exp := time.Now().Add(time.Hour)
	token, handle := reg.Mint("acme", "worker", "t2", exp)

	inv, err := reg.Resolve(token, "acme")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Record an intent directly into the sink.
	inv.sink.Record(port.ActionIntent{Kind: "telegram_send", Payload: map[string]any{"body": "hello"}}) //nolint:errcheck

	intents := handle.Drain()
	if len(intents) != 1 {
		t.Fatalf("Drain: expected 1 intent, got %d", len(intents))
	}
	if intents[0].Kind != "telegram_send" {
		t.Errorf("Drain: expected Kind=telegram_send, got %q", intents[0].Kind)
	}
}

func TestRegistry_UnknownTokenRejected(t *testing.T) {
	reg := NewRegistry()

	_, err := reg.Resolve("never-minted-token", "acme")
	if err == nil {
		t.Error("Resolve with unknown token: expected error, got nil")
	}
}

func TestRegistry_DrainedTokenBehavesAsUnknown(t *testing.T) {
	reg := NewRegistry()
	exp := time.Now().Add(time.Hour)
	token, handle := reg.Mint("acme", "worker", "t3", exp)

	// First Drain deletes the entry.
	first := handle.Drain()
	if len(first) != 0 {
		t.Errorf("first Drain: expected 0 intents, got %d", len(first))
	}

	// Resolve after Drain must fail.
	_, err := reg.Resolve(token, "acme")
	if err == nil {
		t.Error("Resolve after Drain: expected error, got nil")
	}

	// Second Drain must be idempotent (no panic, returns empty).
	second := handle.Drain()
	if second != nil && len(second) != 0 {
		t.Errorf("second Drain: expected empty/nil, got %v", second)
	}
}

func TestRegistry_ForeignTenantRejected(t *testing.T) {
	reg := NewRegistry()
	exp := time.Now().Add(time.Hour)
	token, _ := reg.Mint("acme", "worker", "t4", exp)

	// Resolve with a different tenant must fail.
	_, err := reg.Resolve(token, "beta")
	if err == nil {
		t.Error("Resolve with foreign tenant: expected error, got nil")
	}
}

// TestRegistry_ContactedTracksTokenVerification proves the registry records whether the
// minted token was ever presented to the MCP server. TokenVerifier runs for every
// authenticated HTTP request, including the MCP initialize handshake, so it is the
// truest available signal that the CLI actually reached the endpoint with our token.
//
// Without this signal an adapter cannot distinguish "the agent chose not to call a tool"
// from "the agent never reached the MCP server at all": both produce an empty sink.
// Contacted must also survive Drain, because the adapter reads it after draining.
func TestRegistry_ContactedTracksTokenVerification(t *testing.T) {
	reg := NewRegistry()
	token, handle := reg.Mint("acme", "worker", "t5", time.Now().Add(time.Hour))

	if handle.Contacted() {
		t.Error("freshly minted handle: expected Contacted()=false, got true")
	}

	verify := reg.TokenVerifier()
	if _, err := verify(context.Background(), token, nil); err != nil {
		t.Fatalf("TokenVerifier: unexpected error: %v", err)
	}
	if !handle.Contacted() {
		t.Error("after a successful token verification: expected Contacted()=true, got false")
	}

	handle.Drain()
	if !handle.Contacted() {
		t.Error("after Drain: expected Contacted() to remain true, got false")
	}
}

// TestRegistry_ContactedStaysFalseForRejectedToken verifies that a failed verification
// (unknown token) never marks any invocation as contacted.
func TestRegistry_ContactedStaysFalseForRejectedToken(t *testing.T) {
	reg := NewRegistry()
	_, handle := reg.Mint("acme", "worker", "t6", time.Now().Add(time.Hour))

	verify := reg.TokenVerifier()
	if _, err := verify(context.Background(), "never-minted-token", nil); err == nil {
		t.Fatal("TokenVerifier with unknown token: expected error, got nil")
	}
	if handle.Contacted() {
		t.Error("after a rejected verification: expected Contacted()=false, got true")
	}
}

func TestRegistry_ParallelMintAndDrain(t *testing.T) {
	reg := NewRegistry()
	const goroutines = 50

	var wg sync.WaitGroup
	type result struct {
		intents []port.ActionIntent
	}
	results := make([]result, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			exp := time.Now().Add(time.Hour)
			token, handle := reg.Mint("acme", "worker", "parallel-task", exp)

			inv, err := reg.Resolve(token, "acme")
			if err != nil {
				t.Errorf("goroutine %d: Resolve: %v", i, err)
				return
			}
			inv.sink.Record(port.ActionIntent{Kind: "test", Payload: nil}) //nolint:errcheck

			results[i].intents = handle.Drain()
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if len(r.intents) != 1 {
			t.Errorf("goroutine %d: expected 1 intent, got %d", i, len(r.intents))
		}
	}
}
