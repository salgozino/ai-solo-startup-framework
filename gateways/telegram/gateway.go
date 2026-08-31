// Package telegram provides the Telegram Bot API port adapter for port.Gateway.
// It is outbound-only: no inbound path, no polling, no webhook.
//
// The recipient is always the configured owner; caller-supplied recipient fields
// in the message are silently ignored (threat-matrix case f).
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// ErrInlineToken is returned when the caller passes an actual token value (containing ':')
// instead of an env-var name. This mirrors the config-layer inline-secret guard.
var ErrInlineToken = errors.New("telegram: tokenEnvName appears to be an inline token value; pass the env-var name (e.g. TELEGRAM_BOT_TOKEN), not the secret")

// envVarName matches valid environment variable names. Anything else cannot be an env-var
// name and must be an inline secret value.
var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// defaultBase is the Telegram Bot API base URL, overridable in tests via NewWithBase.
const defaultBase = "https://api.telegram.org"

// httpClient is the HTTP client used for all Telegram API calls.
// A 10-second timeout prevents goroutine leaks if the API hangs.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// TelegramGateway implements port.Gateway via the Telegram Bot API.
// Construct via New or NewWithBase; zero value is invalid.
type TelegramGateway struct {
	token   string // resolved at construction from env
	ownerID string // resolved at construction from env
	base    string // API base URL (overridable in tests)
}

// compile-time interface check.
var _ port.Gateway = (*TelegramGateway)(nil)

// New constructs a TelegramGateway by reading tokenEnvName and ownerEnvName from the
// environment. Both env-var names must be valid identifiers (not inline secret values).
// Returns an error if either env var is unset or if an inline token is detected.
func New(tokenEnvName, ownerEnvName string) (*TelegramGateway, error) {
	return NewWithBase(tokenEnvName, ownerEnvName, defaultBase)
}

// NewWithBase is like New but overrides the API base URL. Used in tests to point at a
// stub HTTP server instead of api.telegram.org.
func NewWithBase(tokenEnvName, ownerEnvName, base string) (*TelegramGateway, error) {
	// Reject inline token: a real bot token contains ':' and never matches an env-var name.
	if !envVarName.MatchString(tokenEnvName) {
		return nil, ErrInlineToken
	}
	if !envVarName.MatchString(ownerEnvName) {
		return nil, fmt.Errorf("telegram: ownerEnvName %q is not a valid env-var name", ownerEnvName)
	}

	token := os.Getenv(tokenEnvName)
	if token == "" {
		return nil, fmt.Errorf("telegram: env var %q is unset or empty; cannot construct gateway", tokenEnvName)
	}

	ownerID := os.Getenv(ownerEnvName)
	if ownerID == "" {
		return nil, fmt.Errorf("telegram: env var %q is unset or empty; cannot construct gateway", ownerEnvName)
	}

	return &TelegramGateway{
		token:   token,
		ownerID: ownerID,
		base:    strings.TrimRight(base, "/"),
	}, nil
}

// Send delivers msg over the Telegram Bot API to the configured owner.
// The recipient is always the owner — any recipient-like field in msg.Metadata is ignored.
// Returns an error if the channel is unknown, the HTTP request fails, or the API reports
// a non-ok response. Never panics.
func (g *TelegramGateway) Send(ctx context.Context, msg port.OutboundMessage) error {
	if err := port.ValidateChannel(msg.Channel); err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", g.base, g.token)

	form := url.Values{}
	form.Set("chat_id", g.ownerID) // always owner; caller recipient ignored
	form.Set("text", msg.Body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("telegram: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: HTTP request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return fmt.Errorf("telegram: decode response (HTTP %d): %w", resp.StatusCode, err)
	}
	if !result.OK {
		return fmt.Errorf("telegram: API error (HTTP %d): %s", resp.StatusCode, result.Description)
	}
	return nil
}
