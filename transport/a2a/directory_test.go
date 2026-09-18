package a2a

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestPeerDirectory_UnknownRole verifies that looking up a role never declared
// at construction returns ErrUnknownRole naming that role, never a panic.
// Satisfies: peer-directory "Looking up an undeclared role returns a named
// 'unknown role' error".
func TestPeerDirectory_UnknownRole(t *testing.T) {
	dir, err := NewPeerDirectory([]string{"ceo", "engineer"})
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}

	_, err = dir.BaseURL("designer")
	if err == nil {
		t.Fatal("expected an error for an undeclared role, got nil")
	}
	if !errors.Is(err, ErrUnknownRole) {
		t.Errorf("expected ErrUnknownRole, got %v", err)
	}
	if !strings.Contains(err.Error(), "designer") {
		t.Errorf("expected the error to name the role %q, got %q", "designer", err.Error())
	}
}

// TestPeerDirectory_NotYetBound verifies that looking up a role declared at
// construction but not yet bound returns ErrPeerNotRegistered naming that
// role, never a panic and never a zero-value URL.
// Satisfies: peer-directory "Looking up a declared-but-unbound role returns a
// named 'not yet registered' error".
func TestPeerDirectory_NotYetBound(t *testing.T) {
	dir, err := NewPeerDirectory([]string{"ceo", "engineer"})
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}

	_, err = dir.BaseURL("engineer")
	if err == nil {
		t.Fatal("expected an error for a declared-but-unbound role, got nil")
	}
	if !errors.Is(err, ErrPeerNotRegistered) {
		t.Errorf("expected ErrPeerNotRegistered, got %v", err)
	}
	if !strings.Contains(err.Error(), "engineer") {
		t.Errorf("expected the error to name the role %q, got %q", "engineer", err.Error())
	}
}

// TestPeerDirectory_BoundRoleReturnsURL verifies that a role bound via Bind
// resolves to exactly the URL it was bound with.
// Satisfies: peer-directory "A bound agent's role is registered with its live
// base URL".
func TestPeerDirectory_BoundRoleReturnsURL(t *testing.T) {
	dir, err := NewPeerDirectory([]string{"ceo", "engineer"})
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}

	const wantURL = "http://127.0.0.1:54321"
	if err := dir.Bind("engineer", wantURL); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	got, err := dir.BaseURL("engineer")
	if err != nil {
		t.Fatalf("BaseURL: %v", err)
	}
	if got != wantURL {
		t.Errorf("BaseURL(%q) = %q, want %q", "engineer", got, wantURL)
	}
}

// TestNewPeerDirectory_DuplicateRoleFails verifies that constructing a
// directory with a duplicate role in the input list fails with an error
// naming the duplicate, instead of silently accepting a last-one-wins role
// set.
// Satisfies: company-as-code "Two agents declaring the same role fail
// materialize with a named error" (the directory-level guard that error
// depends on).
func TestNewPeerDirectory_DuplicateRoleFails(t *testing.T) {
	_, err := NewPeerDirectory([]string{"ceo", "engineer", "ceo"})
	if err == nil {
		t.Fatal("expected an error for a duplicate role, got nil")
	}
	if !strings.Contains(err.Error(), "ceo") {
		t.Errorf("expected the error to name the duplicate role %q, got %q", "ceo", err.Error())
	}
}

// TestPeerDirectory_RebindFails verifies that Bind rejects a second call for
// a role already bound — the directory publishes each role's URL write-once,
// per design D7's concurrency model.
func TestPeerDirectory_RebindFails(t *testing.T) {
	dir, err := NewPeerDirectory([]string{"engineer"})
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}
	if err := dir.Bind("engineer", "http://127.0.0.1:1"); err != nil {
		t.Fatalf("first Bind: %v", err)
	}

	err = dir.Bind("engineer", "http://127.0.0.1:2")
	if err == nil {
		t.Fatal("expected an error rebinding an already-bound role, got nil")
	}
	if !strings.Contains(err.Error(), "engineer") {
		t.Errorf("expected the error to name the role %q, got %q", "engineer", err.Error())
	}
}

// TestPeerDirectory_ConcurrentBindAndBaseURL runs Bind and BaseURL from
// parallel goroutines to prove the directory's sync.RWMutex actually guards
// concurrent access (design D7: "Task goroutines call BaseURL (read lock)
// while the materializeAgents loop still calls Bind (write lock)"). Only
// meaningful under `go test -race`.
func TestPeerDirectory_ConcurrentBindAndBaseURL(t *testing.T) {
	roles := []string{"ceo", "engineer", "designer", "qa"}
	dir, err := NewPeerDirectory(roles)
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}

	var wg sync.WaitGroup
	for i, role := range roles {
		wg.Add(2)
		go func(role, url string) {
			defer wg.Done()
			_ = dir.Bind(role, url)
		}(role, fmt.Sprintf("http://127.0.0.1:%d", 10000+i))
		go func(role string) {
			defer wg.Done()
			_, _ = dir.BaseURL(role)
		}(role)
	}
	wg.Wait()
}
