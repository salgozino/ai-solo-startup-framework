// Package telegram_test contains unit tests for TelegramGateway.
// Task 6.1: RED tests written first — all will fail until gateway.go is created.
package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/gateways/telegram"
)

// stubServer starts a test HTTP server that responds with a fixed JSON payload.
// If fail is true it returns HTTP 500 so Send must return error.
func stubServer(t *testing.T, fail bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, `{"ok":false,"error_code":400,"description":"Bad Request"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 42}}) //nolint:errcheck
	}))
}

// TestNew_NoToken verifies that New returns an error when token env var is unset.
// Threat-matrix case (a): gateway fails closed without token.
func TestNew_NoToken(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_OWNER_ID", "12345")

	_, err := telegram.New("TELEGRAM_BOT_TOKEN", "TELEGRAM_OWNER_ID")
	if err == nil {
		t.Fatal("expected error when token env var is empty, got nil")
	}
}

// TestNew_NoOwner verifies that New returns an error when owner id env var is unset.
func TestNew_NoOwner(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "fake-token-abc123")
	t.Setenv("TELEGRAM_OWNER_ID", "")

	_, err := telegram.New("TELEGRAM_BOT_TOKEN", "TELEGRAM_OWNER_ID")
	if err == nil {
		t.Fatal("expected error when owner env var is empty, got nil")
	}
}

// TestNew_InlineTokenRejected verifies that a value that looks like a real Telegram
// bot token (contains ':') is rejected at construction — the field must be an env-var
// name, not the actual secret value. Threat-matrix case (b): inline token rejected at load.
//
// Note: TelegramGateway.New receives env-var *names* (like "TELEGRAM_BOT_TOKEN"), not
// raw token strings. If a caller passes the literal token value as the envName argument,
// that value contains ':' which is not a valid env-var name — New must reject it.
func TestNew_InlineTokenRejected(t *testing.T) {
	// Pass the actual token string as if it were the env-var name.
	// A real Telegram bot token looks like "1234567890:AABBcc...".
	inlineToken := "1234567890:AABBccDDee"

	_, err := telegram.New(inlineToken, "TELEGRAM_OWNER_ID")
	if err == nil {
		t.Fatalf("expected error when inline token passed as env-var name, got nil")
	}
	if !errors.Is(err, telegram.ErrInlineToken) {
		t.Fatalf("expected ErrInlineToken, got: %v", err)
	}
}

// TestSend_OwnerWinsOverCallerRecipient verifies that the message is always sent to
// the configured owner, regardless of any recipient-like field in the message.
// Threat-matrix case (f): caller-supplied recipient has no effect.
//
// The OutboundMessage.Metadata may carry a "recipient" key set by the caller; the
// gateway must ignore it and route to the owner extracted at construction.
func TestSend_OwnerWinsOverCallerRecipient(t *testing.T) {
	const ownerID = "777000"
	t.Setenv("TELEGRAM_BOT_TOKEN", "fake-token")
	t.Setenv("TELEGRAM_OWNER_ID", ownerID)

	// Capture which chat_id is sent to the stub server.
	var capturedChatID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err == nil {
			capturedChatID = r.FormValue("chat_id")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1}}) //nolint:errcheck
	}))
	defer srv.Close()

	gw, err := telegram.NewWithBase("TELEGRAM_BOT_TOKEN", "TELEGRAM_OWNER_ID", srv.URL)
	if err != nil {
		t.Fatalf("NewWithBase: %v", err)
	}

	msg := port.OutboundMessage{
		Channel:  "telegram",
		Body:     "hello owner",
		Metadata: map[string]any{"recipient": "attacker-id-999"},
	}
	if err := gw.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if capturedChatID != ownerID {
		t.Fatalf("expected chat_id=%q (owner), got %q (caller may have hijacked recipient)", ownerID, capturedChatID)
	}
}

// TestSend_APIFailure verifies that a failed Telegram API call returns an error
// and never panics. Threat-matrix case (d): failed API call → error, never panic.
func TestSend_APIFailure(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "fake-token")
	t.Setenv("TELEGRAM_OWNER_ID", "12345")

	srv := stubServer(t, true /* fail */)
	defer srv.Close()

	gw, err := telegram.NewWithBase("TELEGRAM_BOT_TOKEN", "TELEGRAM_OWNER_ID", srv.URL)
	if err != nil {
		t.Fatalf("NewWithBase: %v", err)
	}

	msg := port.OutboundMessage{Channel: "telegram", Body: "hello"}
	err = gw.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error on API failure, got nil")
	}
}

// TestSend_UnknownChannel verifies that Send rejects channels not on the allow-list.
func TestSend_UnknownChannel(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "fake-token")
	t.Setenv("TELEGRAM_OWNER_ID", "12345")

	srv := stubServer(t, false)
	defer srv.Close()

	gw, err := telegram.NewWithBase("TELEGRAM_BOT_TOKEN", "TELEGRAM_OWNER_ID", srv.URL)
	if err != nil {
		t.Fatalf("NewWithBase: %v", err)
	}

	msg := port.OutboundMessage{Channel: "slack", Body: "wrong channel"}
	err = gw.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for unknown channel, got nil")
	}
}

// TestSend_ContextCanceled verifies that a canceled context surfaces as an error.
func TestSend_ContextCanceled(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "fake-token")
	t.Setenv("TELEGRAM_OWNER_ID", "12345")

	// Slow server that won't respond before the canceled context times out.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	gw, err := telegram.NewWithBase("TELEGRAM_BOT_TOKEN", "TELEGRAM_OWNER_ID", srv.URL)
	if err != nil {
		t.Fatalf("NewWithBase: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	msg := port.OutboundMessage{Channel: "telegram", Body: "should not arrive"}
	err = gw.Send(ctx, msg)
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
}
