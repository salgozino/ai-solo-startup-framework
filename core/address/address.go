// Package address defines A2AAddress, the only valid lookup key for agents.
// An address encodes both agent name and tenant: "{agent-name}/{tenant}".
// Constructing an address without a non-empty tenant is a compile-time-impossible
// operation via the API: New enforces the invariant at construction time and Parse
// enforces it at parse time, so agent-name-alone is never a valid key.
package address

import (
	"errors"
	"fmt"
	"strings"
)

// A2AAddress is the canonical lookup key for an agent within a tenant.
// Format: "{agent-name}/{tenant}". The underlying string type is not exported
// as a bare string alias so callers cannot construct one without going through
// New or Parse, making the wrong thing hard to express.
//
// Zero value ("") is explicitly invalid; use New or Parse to obtain a valid address.
type A2AAddress string

// ErrEmptyTenant is returned when tenant is absent or empty.
var ErrEmptyTenant = errors.New("address: tenant must not be empty")

// ErrEmptyName is returned when agent name is absent or empty.
var ErrEmptyName = errors.New("address: agent name must not be empty")

// ErrInvalidFormat is returned when a string does not match "{name}/{tenant}".
var ErrInvalidFormat = errors.New("address: invalid format, expected \"{agent-name}/{tenant}\"")

// New constructs an A2AAddress from name and tenant.
// Returns ErrEmptyName if name is empty, ErrEmptyTenant if tenant is empty.
// The name must not contain a slash.
func New(name, tenant string) (A2AAddress, error) {
	if name == "" {
		return "", ErrEmptyName
	}
	if strings.Contains(name, "/") {
		return "", fmt.Errorf("address: agent name must not contain '/': %q", name)
	}
	if tenant == "" {
		return "", ErrEmptyTenant
	}
	if strings.Contains(tenant, "/") {
		return "", fmt.Errorf("address: tenant must not contain '/': %q", tenant)
	}
	return A2AAddress(name + "/" + tenant), nil
}

// Parse reconstructs an A2AAddress from a previously serialized string.
// Accepts exactly one slash separating a non-empty name from a non-empty tenant.
func Parse(s string) (A2AAddress, error) {
	idx := strings.Index(s, "/")
	if idx < 0 {
		return "", ErrInvalidFormat
	}
	name := s[:idx]
	tenant := s[idx+1:]
	if name == "" {
		return "", ErrEmptyName
	}
	if tenant == "" {
		return "", ErrEmptyTenant
	}
	// Reject extra slashes in tenant (would allow ambiguous parsing).
	if strings.Contains(tenant, "/") {
		return "", ErrInvalidFormat
	}
	return A2AAddress(s), nil
}

// Name returns the agent-name segment of the address.
func (a A2AAddress) Name() string {
	idx := strings.Index(string(a), "/")
	if idx < 0 {
		return ""
	}
	return string(a)[:idx]
}

// Tenant returns the tenant segment of the address.
func (a A2AAddress) Tenant() string {
	idx := strings.Index(string(a), "/")
	if idx < 0 {
		return ""
	}
	return string(a)[idx+1:]
}

// String implements fmt.Stringer.
func (a A2AAddress) String() string { return string(a) }
