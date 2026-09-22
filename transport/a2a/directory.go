package a2a

import (
	"errors"
	"fmt"
	"sync"
)

// ErrUnknownRole means the role is not declared in company.yaml. Permanent —
// no future Bind call for this role will ever succeed.
var ErrUnknownRole = errors.New("unknown role")

// ErrPeerNotRegistered means the role is declared but its agent's A2A server
// has not finished binding yet. Transient — a caller retrying later may
// observe a bound URL once materialize progresses further.
var ErrPeerNotRegistered = errors.New("peer not yet registered")

// PeerDirectory maps agent role -> live A2A base URL. The declared role set
// is fixed at construction from company.yaml; base URLs are published one at
// a time as each agent's server finishes binding. Splitting the declared and
// bound sets lets callers distinguish "no such role, ever" from "not up yet,
// retry" — a distinction the (deferred) delegation watcher depends on.
// See design D7.
type PeerDirectory struct {
	mu       sync.RWMutex
	declared map[string]struct{}
	bound    map[string]string
}

// NewPeerDirectory constructs a PeerDirectory declaring exactly roles as
// valid lookup targets, none of them bound yet. Returns an error if roles
// contains a duplicate entry — a duplicate role must never silently produce
// a last-one-wins directory.
func NewPeerDirectory(roles []string) (*PeerDirectory, error) {
	declared := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if _, exists := declared[role]; exists {
			return nil, fmt.Errorf("transport/a2a: duplicate role %q declared for peer directory", role)
		}
		declared[role] = struct{}{}
	}
	return &PeerDirectory{
		declared: declared,
		bound:    make(map[string]string, len(roles)),
	}, nil
}

// Bind publishes baseURL as role's live A2A endpoint. Returns an error
// wrapping ErrUnknownRole if role was never declared at construction, or a
// plain error if role is already bound — each role publishes its URL exactly
// once, matching the write-once-per-role concurrency model in design D7.
func (d *PeerDirectory) Bind(role, baseURL string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.declared[role]; !ok {
		return fmt.Errorf("transport/a2a: cannot bind undeclared role %q: %w", role, ErrUnknownRole)
	}
	if _, ok := d.bound[role]; ok {
		return fmt.Errorf("transport/a2a: role %q is already bound", role)
	}
	d.bound[role] = baseURL
	return nil
}

// BaseURL returns role's live A2A base URL. Returns an error wrapping
// ErrUnknownRole if role was never declared, or ErrPeerNotRegistered if role
// is declared but has not bound yet. Never reads a nil map, never returns a
// zero-value URL silently, never panics.
func (d *PeerDirectory) BaseURL(role string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if _, ok := d.declared[role]; !ok {
		return "", fmt.Errorf("transport/a2a: role %q: %w", role, ErrUnknownRole)
	}
	baseURL, ok := d.bound[role]
	if !ok {
		return "", fmt.Errorf("transport/a2a: role %q: %w", role, ErrPeerNotRegistered)
	}
	return baseURL, nil
}
