package policy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// tokenStore mints and validates opaque approval tokens.
// Tokens are random 128-bit values tracked in-memory.
// Only tokens minted by this instance are accepted.
type tokenStore struct {
	mu     sync.Mutex
	issued map[string]struct{}
}

func newTokenStore() *tokenStore {
	return &tokenStore{issued: make(map[string]struct{})}
}

// mint generates a new random token, records it, and returns it.
func (ts *tokenStore) mint() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is exceptional; panic is appropriate here because
		// the system cannot safely continue without a secure token source.
		panic(fmt.Sprintf("policy: token mint: %v", err))
	}
	token := hex.EncodeToString(b)
	ts.mu.Lock()
	ts.issued[token] = struct{}{}
	ts.mu.Unlock()
	return token
}

// validate returns nil if token was minted by this store, otherwise an error.
// On success, the token is consumed (deleted) to prevent replay.
func (ts *tokenStore) validate(token string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	_, ok := ts.issued[token]
	if !ok {
		return fmt.Errorf("policy: invalid approval token")
	}
	delete(ts.issued, token)
	return nil
}
