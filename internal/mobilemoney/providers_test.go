package mobilemoney

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNormalizeProviderCanonicalizesKnownNames(t *testing.T) {
	if got := NormalizeProvider("OrangeMoney"); got != "orange_money" {
		t.Fatalf("unexpected provider normalization: %s", got)
	}
	if got := NormalizeProvider("moov-money"); got != "moov_money" {
		t.Fatalf("unexpected provider normalization: %s", got)
	}
}

func TestLiveHTTPProviderBuildsSignedRequest(t *testing.T) {
	var capturedBody []byte

	provider := NewLiveHTTPProviderWithClient(
		"OrangeMoney",
		LiveConfig{
			BaseURL:    "https://aggregator.test",
			APIKey:     "api-key-123",
			HMACSecret: "secret-xyz",
		},
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://aggregator.test/payouts" {
				t.Fatalf("unexpected url %s", req.URL.String())
			}
			if got := req.Header.Get("X-Provider"); got != "orange_money" {
				t.Fatalf("unexpected provider header %s", got)
			}
			if got := req.Header.Get("X-API-Key"); got != "api-key-123" {
				t.Fatalf("unexpected api key header %s", got)
			}
			if got := req.Header.Get("X-Idempotency-Key"); got != "idem-001" {
				t.Fatalf("unexpected idempotency header %s", got)
			}

			var err error
			capturedBody, err = io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			expectedSig := "sha256=" + signPayload(capturedBody, "secret-xyz")
			if got := req.Header.Get("X-Signature"); got != expectedSig {
				t.Fatalf("unexpected signature header %s", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewBufferString(
					`{"external_ref":"agg-ref-001","status":"accepted"}`,
				)),
				Header: make(http.Header),
			}, nil
		}),
	)

	result, err := provider.SendPayout(context.Background(), PayoutCommand{
		CycleID:             "cycle-001",
		GroupID:             "group-001",
		BeneficiaryID:       "beneficiary-001",
		BeneficiaryMsisdn:   "+22670000001",
		BeneficiaryProvider: "OrangeMoney",
		AmountMinor:         10000,
		IdempotencyKey:      "idem-001",
		InitiatedAt:         NewFlexibleTimestampString("2026-04-04T17:00:00Z"),
	})
	if err != nil {
		t.Fatalf("send payout: %v", err)
	}
	if result.ExternalRef != "agg-ref-001" {
		t.Fatalf("unexpected external ref %s", result.ExternalRef)
	}
	if result.ConfirmImmediately {
		t.Fatal("live provider should not confirm immediately")
	}

	var payload AggregatorPayoutRequest
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Provider != "orange_money" {
		t.Fatalf("unexpected provider payload %s", payload.Provider)
	}
	if payload.Payout.CycleID != "cycle-001" {
		t.Fatalf("unexpected cycle id %+v", payload.Payout)
	}
}

func TestLiveHTTPProviderRequiresConfig(t *testing.T) {
	provider := NewLiveHTTPProvider("wave", LiveConfig{})

	_, err := provider.SendPayout(context.Background(), PayoutCommand{IdempotencyKey: "idem"})
	if err == nil {
		t.Fatal("expected configuration error")
	}
}

func TestPayoutCommandAcceptsLegacyTupleTimestamp(t *testing.T) {
	var cmd PayoutCommand
	err := json.Unmarshal([]byte(`{
		"cycle_id":"cycle-legacy",
		"idempotency_key":"idem-legacy",
		"initiated_at":[2026,94,18,32,55,157816663,0,0,0]
	}`), &cmd)
	if err != nil {
		t.Fatalf("unmarshal legacy timestamp: %v", err)
	}

	if got := cmd.InitiatedAt.String(); got != "[2026,94,18,32,55,157816663,0,0,0]" {
		t.Fatalf("unexpected legacy timestamp representation %s", got)
	}
}
