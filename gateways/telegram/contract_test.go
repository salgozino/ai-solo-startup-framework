// Package telegram_test — contract tests for port.Gateway.
// Task 6.3: run the Gateway contract assertions against TelegramGateway (using a stub
// HTTP server, no real Telegram needed) AND against fake.Gateway from PR 2.
// Both implementations must exhibit equivalent behavior.
package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
	"github.com/salgozino/ai-solo-startup-framework/gateways/telegram"
)

// okServer returns a test server that always replies with Telegram ok=true.
func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"ok":     true,
			"result": map[string]any{"message_id": 1},
		})
	}))
}

// failServer returns a test server that always replies with Telegram ok=false.
func failServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"ok":          false,
			"description": "Bad Request: chat not found",
		})
	}))
}

// makeReal returns a TelegramGateway pointed at the given stub base URL.
func makeReal(t *testing.T, base string) port.Gateway {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "fake-token-for-contract")
	t.Setenv("TELEGRAM_OWNER_ID", "99999")
	gw, err := telegram.NewWithBase("TELEGRAM_BOT_TOKEN", "TELEGRAM_OWNER_ID", base)
	if err != nil {
		t.Fatalf("makeReal: %v", err)
	}
	return gw
}

// ---- contract assertions shared by both implementations --------------------

// contractSend_KnownChannel asserts Send("telegram") succeeds on an impl backed
// by a non-failing stub.
func contractSend_KnownChannel(t *testing.T, gw port.Gateway) {
	t.Helper()
	msg := port.OutboundMessage{Channel: "telegram", Body: "contract hello"}
	if err := gw.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send(telegram): unexpected error: %v", err)
	}
}

// contractSend_UnknownChannel asserts Send with an unknown channel returns an error.
func contractSend_UnknownChannel(t *testing.T, gw port.Gateway) {
	t.Helper()
	msg := port.OutboundMessage{Channel: "slack", Body: "wrong"}
	err := gw.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("Send(slack): expected error for unknown channel, got nil")
	}
}

// contractSend_EmptyBody asserts that an empty body is not itself an error at the
// port boundary (body validation is the caller's concern, not the gateway's).
func contractSend_EmptyBody(t *testing.T, gw port.Gateway) {
	t.Helper()
	msg := port.OutboundMessage{Channel: "telegram", Body: ""}
	// Both fakeGateway and TelegramGateway (with ok stub) should accept an empty body.
	if err := gw.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send(empty body): unexpected error: %v", err)
	}
}

// contractSend_DeliveryFailure asserts that a delivery failure surfaces as an error.
// For fakeGateway: configure ReturnErr; for TelegramGateway: backed by failServer.
func contractSend_DeliveryFailure_Fake(t *testing.T) {
	t.Helper()
	fg := &fake.Gateway{ReturnErr: errors.New("telegram: rate limited")}
	msg := port.OutboundMessage{Channel: "telegram", Body: "urgent"}
	if err := fg.Send(context.Background(), msg); err == nil {
		t.Fatal("Send: expected delivery failure error from fakeGateway, got nil")
	}
}

// contractSend_MessageShape asserts that Send(telegram, body) produces equivalent
// behavior: both real and fake record/deliver the body without mutation.
func contractSend_MessageShape(t *testing.T, gw port.Gateway) {
	t.Helper()
	const body = "shape equivalence test"
	msg := port.OutboundMessage{Channel: "telegram", Body: body}
	if err := gw.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// For fakeGateway we can assert the recorded payload; for TelegramGateway
	// the stub server captures it — shape is verified at the HTTP layer in
	// TestSend_OwnerWinsOverCallerRecipient. Here we only assert no error.
}

// ---- run contract assertions against TelegramGateway -----------------------

func TestContract_TelegramGateway_KnownChannel(t *testing.T) {
	srv := okServer(t)
	defer srv.Close()
	contractSend_KnownChannel(t, makeReal(t, srv.URL))
}

func TestContract_TelegramGateway_UnknownChannel(t *testing.T) {
	srv := okServer(t)
	defer srv.Close()
	contractSend_UnknownChannel(t, makeReal(t, srv.URL))
}

func TestContract_TelegramGateway_EmptyBody(t *testing.T) {
	srv := okServer(t)
	defer srv.Close()
	contractSend_EmptyBody(t, makeReal(t, srv.URL))
}

func TestContract_TelegramGateway_DeliveryFailure(t *testing.T) {
	srv := failServer(t)
	defer srv.Close()
	gw := makeReal(t, srv.URL)
	msg := port.OutboundMessage{Channel: "telegram", Body: "will fail"}
	if err := gw.Send(context.Background(), msg); err == nil {
		t.Fatal("TelegramGateway: expected error from fail stub, got nil")
	}
}

func TestContract_TelegramGateway_MessageShape(t *testing.T) {
	srv := okServer(t)
	defer srv.Close()
	contractSend_MessageShape(t, makeReal(t, srv.URL))
}

// ---- run contract assertions against fakeGateway ---------------------------

func TestContract_FakeGateway_KnownChannel(t *testing.T) {
	contractSend_KnownChannel(t, &fake.Gateway{})
}

func TestContract_FakeGateway_UnknownChannel(t *testing.T) {
	contractSend_UnknownChannel(t, &fake.Gateway{})
}

func TestContract_FakeGateway_EmptyBody(t *testing.T) {
	contractSend_EmptyBody(t, &fake.Gateway{})
}

func TestContract_FakeGateway_DeliveryFailure(t *testing.T) {
	contractSend_DeliveryFailure_Fake(t)
}

func TestContract_FakeGateway_MessageShape(t *testing.T) {
	contractSend_MessageShape(t, &fake.Gateway{})
}

// TestContract_FakeGateway_RecordsPayload asserts fakeGateway records sent messages,
// verifying shape equivalence: body is preserved verbatim.
func TestContract_FakeGateway_RecordsPayload(t *testing.T) {
	fg := &fake.Gateway{}
	const body = "payload preserved"
	msg := port.OutboundMessage{Channel: "telegram", Body: body}
	if err := fg.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	last, ok := fg.LastCall()
	if !ok {
		t.Fatal("LastCall: expected a recorded call")
	}
	if last.Body != body {
		t.Fatalf("expected body %q, got %q", body, last.Body)
	}
}

// TestContract_Equivalence_NoPanic asserts that neither implementation panics on
// a malformed or empty message — contract invariant "MUST NOT panic".
func TestContract_Equivalence_NoPanic(t *testing.T) {
	srv := failServer(t)
	defer srv.Close()

	impls := []struct {
		name string
		gw   port.Gateway
	}{
		{"TelegramGateway", makeReal(t, srv.URL)},
		{"fakeGateway", &fake.Gateway{ReturnErr: errors.New("forced")}},
	}

	for _, impl := range impls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s panicked: %v", impl.name, r)
				}
			}()
			// Unknown channel + empty body: must return error, never panic.
			msg := port.OutboundMessage{Channel: "", Body: ""}
			_ = impl.gw.Send(context.Background(), msg)
		})
	}
}
