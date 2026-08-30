package address_test

import (
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		agent   string
		tenant  string
		want    string
		wantErr error
	}{
		{
			name:   "valid address",
			agent:  "ceo",
			tenant: "acme",
			want:   "ceo/acme",
		},
		{
			name:   "valid address with hyphen",
			agent:  "my-worker",
			tenant: "beta",
			want:   "my-worker/beta",
		},
		{
			name:    "empty tenant rejected",
			agent:   "ceo",
			tenant:  "",
			wantErr: address.ErrEmptyTenant,
		},
		{
			name:    "empty name rejected",
			agent:   "",
			tenant:  "acme",
			wantErr: address.ErrEmptyName,
		},
		{
			name:    "both empty rejected",
			agent:   "",
			tenant:  "",
			wantErr: address.ErrEmptyName,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := address.New(tc.agent, tc.tenant)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("New(%q, %q) = %q, nil; want error %v", tc.agent, tc.tenant, got, tc.wantErr)
				}
				if err != tc.wantErr {
					t.Fatalf("New(%q, %q) error = %v; want %v", tc.agent, tc.tenant, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q, %q) unexpected error: %v", tc.agent, tc.tenant, err)
			}
			if got.String() != tc.want {
				t.Errorf("New(%q, %q) = %q; want %q", tc.agent, tc.tenant, got, tc.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{
			name:  "valid roundtrip",
			input: "ceo/acme",
			want:  "ceo/acme",
		},
		{
			name:  "worker roundtrip",
			input: "worker/beta",
			want:  "worker/beta",
		},
		{
			name:    "agent name only — no slash",
			input:   "ceo",
			wantErr: address.ErrInvalidFormat,
		},
		{
			name:    "missing tenant — trailing slash",
			input:   "ceo/",
			wantErr: address.ErrEmptyTenant,
		},
		{
			name:    "missing name — leading slash",
			input:   "/acme",
			wantErr: address.ErrEmptyName,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: address.ErrInvalidFormat,
		},
		{
			name:    "extra slash is invalid",
			input:   "ceo/acme/extra",
			wantErr: address.ErrInvalidFormat,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := address.Parse(tc.input)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("Parse(%q) = %q, nil; want error %v", tc.input, got, tc.wantErr)
				}
				if err != tc.wantErr {
					t.Fatalf("Parse(%q) error = %v; want %v", tc.input, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.input, err)
			}
			if got.String() != tc.want {
				t.Errorf("Parse(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRoundtrip(t *testing.T) {
	addr, err := address.New("ceo", "acme")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	parsed, err := address.Parse(addr.String())
	if err != nil {
		t.Fatalf("Parse(%q): %v", addr, err)
	}
	if parsed != addr {
		t.Errorf("roundtrip mismatch: got %q, want %q", parsed, addr)
	}
	if parsed.Name() != "ceo" {
		t.Errorf("Name() = %q; want %q", parsed.Name(), "ceo")
	}
	if parsed.Tenant() != "acme" {
		t.Errorf("Tenant() = %q; want %q", parsed.Tenant(), "acme")
	}
}
