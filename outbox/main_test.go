package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"go.uber.org/zap"

	"masecure/internal/mobilemoney"
	"masecure/internal/outboxworker"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMobileMoneyDispatcherConfirmsMockPayout(t *testing.T) {
	var called bool
	dispatcher := NewMobileMoneyDispatcher(
		mobilemoney.NewRegistry(mobilemoney.NewMockProvider("orange_money", 0)),
		"http://kernel.test",
		zap.NewNop(),
	)
	dispatcher.client = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			called = true
			if req.URL.Path != "/internal/payouts/confirmations" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			var payload map[string]string
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if payload["cycle_id"] != "cycle-123" {
				t.Fatalf("unexpected cycle_id: %+v", payload)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"confirmed"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	row := outboxworker.OutboxRow{
		IdempotencyKey: "idem-123",
		Payload: []byte(`{
			"type":"payout_triggered",
			"command":{
				"cycle_id":"cycle-123",
				"beneficiary_provider":"OrangeMoney",
				"idempotency_key":"idem-123"
			}
		}`),
	}

	ref, err := dispatcher.Dispatch(context.Background(), row)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if ref != "mock-ref-idem-123" {
		t.Fatalf("unexpected external ref %s", ref)
	}
	if !called {
		t.Fatal("expected kernel confirmation call")
	}
}

func TestNewMobileMoneyRegistryRoutesKnownProviders(t *testing.T) {
	registry := newMobileMoneyRegistry("mock", mobilemoney.LiveConfig{})

	provider, err := registry.Resolve("OrangeMoney")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}
	if provider.Name() != "orange_money" {
		t.Fatalf("unexpected provider name %s", provider.Name())
	}
}
