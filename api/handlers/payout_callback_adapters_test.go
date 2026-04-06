package handlers

import "testing"

func TestGenericPayoutCallbackAdapterNormalizesStatus(t *testing.T) {
	adapter := genericPayoutCallbackAdapter{}

	payload, err := adapter.Parse([]byte(`{
		"cycle_id":"cycle-001",
		"external_ref":"ext-001",
		"status":"COMPLETED",
		"provider":"OrangeMoney"
	}`))
	if err != nil {
		t.Fatalf("parse payout callback: %v", err)
	}

	if payload.Provider != "orange_money" {
		t.Fatalf("unexpected provider %s", payload.Provider)
	}
	if payload.Status != "success" {
		t.Fatalf("unexpected status %s", payload.Status)
	}
}

func TestOrangeMoneyPayoutCallbackAdapterParsesPayload(t *testing.T) {
	adapter := orangeMoneyPayoutCallbackAdapter{}

	payload, err := adapter.Parse([]byte(`{
		"txn_id":"ext-002",
		"merchant_ref":"cycle-002",
		"status":"FAILED",
		"failure_reason":"network_timeout"
	}`))
	if err != nil {
		t.Fatalf("parse orange payout callback: %v", err)
	}

	if payload.Status != "failed" {
		t.Fatalf("unexpected status %s", payload.Status)
	}
	if payload.Reason != "network_timeout" {
		t.Fatalf("unexpected reason %s", payload.Reason)
	}
}
