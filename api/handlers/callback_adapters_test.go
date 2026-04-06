package handlers

import "testing"

func TestGenericCallbackAdapterNormalizesProviderAndStatus(t *testing.T) {
	adapter := genericCallbackAdapter{}

	payload, err := adapter.Parse([]byte(`{
		"transaction_id":"txn-001",
		"payer_msisdn":"+22670000010",
		"amount":15000,
		"status":"COMPLETED",
		"external_ref":"cycle-001",
		"provider":"OrangeMoney"
	}`))
	if err != nil {
		t.Fatalf("parse generic callback: %v", err)
	}

	if payload.Provider != "orange_money" {
		t.Fatalf("unexpected provider %s", payload.Provider)
	}
	if payload.Status != "success" {
		t.Fatalf("unexpected status %s", payload.Status)
	}
}

func TestOrangeMoneyCallbackAdapterParsesProviderPayload(t *testing.T) {
	adapter := orangeMoneyCallbackAdapter{}

	payload, err := adapter.Parse([]byte(`{
		"txn_id":"orange-001",
		"subscriber_msisdn":"+22670000011",
		"amount_minor":10000,
		"status":"SUCCESS",
		"merchant_ref":"cycle-orange-001"
	}`))
	if err != nil {
		t.Fatalf("parse orange callback: %v", err)
	}

	if payload.ProviderTxRef != "orange-001" {
		t.Fatalf("unexpected transaction ref %+v", payload)
	}
	if payload.Provider != "orange_money" {
		t.Fatalf("unexpected provider %s", payload.Provider)
	}
	if payload.Status != "success" {
		t.Fatalf("unexpected status %s", payload.Status)
	}
}

func TestCallbackAdapterRegistryRejectsUnknownProvider(t *testing.T) {
	registry := NewCallbackAdapterRegistry()

	if _, err := registry.Resolve("unknown-provider"); err == nil {
		t.Fatal("expected unsupported provider error")
	}
}
