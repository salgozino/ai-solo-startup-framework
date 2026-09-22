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

// TestPeerDirectory_BindUndeclaredRoleFails verifies that binding a role that
// was never declared at construction fails with an error wrapping
// ErrUnknownRole and naming that role, and — the part BaseURL alone cannot
// prove — that the rejected call recorded nothing.
// Satisfies: peer-directory "Binding an undeclared role returns a named
// 'unknown role' error" (the Bind-side counterpart of
// TestPeerDirectory_UnknownRole).
func TestPeerDirectory_BindUndeclaredRoleFails(t *testing.T) {
	dir, err := NewPeerDirectory([]string{"ceo", "engineer"})
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}

	const attemptedURL = "http://127.0.0.1:60001"
	err = dir.Bind("designer", attemptedURL)
	if err == nil {
		t.Fatal("expected an error binding an undeclared role, got nil")
	}
	if !errors.Is(err, ErrUnknownRole) {
		t.Errorf("expected ErrUnknownRole, got %v", err)
	}
	if !strings.Contains(err.Error(), "designer") {
		t.Errorf("expected the error to name the role %q, got %q", "designer", err.Error())
	}

	// The rejected role must stay unresolvable, and must never surface the URL
	// the caller attempted to publish.
	got, lookupErr := dir.BaseURL("designer")
	if lookupErr == nil {
		t.Fatalf("expected BaseURL(%q) to keep failing after a rejected Bind, got %q", "designer", got)
	}
	if !errors.Is(lookupErr, ErrUnknownRole) {
		t.Errorf("expected BaseURL to keep returning ErrUnknownRole, got %v", lookupErr)
	}
	if got != "" {
		t.Errorf("BaseURL(%q) = %q after a rejected Bind, want %q", "designer", got, "")
	}

	// BaseURL short-circuits on the declared set, so it returns ErrUnknownRole
	// whether the rejected Bind wrote to the bound map or not. Inspect the map
	// directly (this test lives in package a2a) to prove the failure path
	// recorded nothing at all, as its contract documents.
	dir.mu.RLock()
	recorded, wasRecorded := dir.bound["designer"]
	boundCount := len(dir.bound)
	dir.mu.RUnlock()
	if wasRecorded {
		t.Errorf("rejected Bind recorded %q = %q, want no entry", "designer", recorded)
	}
	if boundCount != 0 {
		t.Errorf("expected no role bound after a rejected Bind, got %d bound role(s)", boundCount)
	}
}

// TestPeerDirectory_ConcurrentBindAndBaseURL runs Bind and BaseURL from
// parallel goroutines to prove the directory's sync.RWMutex actually guards
// concurrent access (design D7: "Task goroutines call BaseURL (read lock)
// while the materializeAgents loop still calls Bind (write lock)").
//
// `-race` only reports data races, so the invariants are asserted explicitly
// and the test stays meaningful without it: every Bind must succeed, every
// concurrent read must return either ErrPeerNotRegistered with an empty URL
// (its own Bind has not landed yet — the only legitimate interleaving) or
// exactly the URL that role was bound with, and once every goroutine has
// finished, every declared role must resolve to its own URL. Results are
// collected per role and asserted after wg.Wait, because t.Fatalf is illegal
// from a non-test goroutine.
func TestPeerDirectory_ConcurrentBindAndBaseURL(t *testing.T) {
	roles := []string{"ceo", "engineer", "designer", "qa"}
	dir, err := NewPeerDirectory(roles)
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}

	want := make(map[string]string, len(roles))
	for i, role := range roles {
		want[role] = fmt.Sprintf("http://127.0.0.1:%d", 10000+i)
	}

	type readResult struct {
		url string
		err error
	}
	// Each goroutine owns exactly one slot, so the slices need no extra
	// synchronisation of their own — wg.Wait happens-before every read below.
	bindErrs := make([]error, len(roles))
	reads := make([]readResult, len(roles))

	var wg sync.WaitGroup
	for i, role := range roles {
		wg.Add(2)
		go func(i int, role string) {
			defer wg.Done()
			bindErrs[i] = dir.Bind(role, want[role])
		}(i, role)
		go func(i int, role string) {
			defer wg.Done()
			url, err := dir.BaseURL(role)
			reads[i] = readResult{url: url, err: err}
		}(i, role)
	}
	wg.Wait()

	for i, role := range roles {
		if bindErrs[i] != nil {
			t.Errorf("Bind(%q, %q) = %v, want nil", role, want[role], bindErrs[i])
		}
	}

	for i, role := range roles {
		got := reads[i]
		switch {
		case got.err == nil:
			// A successful concurrent read must carry the exact bound URL,
			// never a silently empty one.
			if got.url != want[role] {
				t.Errorf("concurrent BaseURL(%q) = %q with nil error, want %q", role, got.url, want[role])
			}
		case errors.Is(got.err, ErrPeerNotRegistered):
			if got.url != "" {
				t.Errorf("concurrent BaseURL(%q) returned URL %q alongside ErrPeerNotRegistered, want %q", role, got.url, "")
			}
		default:
			t.Errorf("concurrent BaseURL(%q) = (%q, %v), want either the bound URL or ErrPeerNotRegistered", role, got.url, got.err)
		}
	}

	// Write-once/lookup invariant: after every Bind has returned, no write was
	// dropped, overwritten, or crossed with another role's URL.
	for _, role := range roles {
		got, err := dir.BaseURL(role)
		if err != nil {
			t.Errorf("BaseURL(%q) after wg.Wait: %v, want the bound URL", role, err)
			continue
		}
		if got != want[role] {
			t.Errorf("BaseURL(%q) after wg.Wait = %q, want %q", role, got, want[role])
		}
	}
}
