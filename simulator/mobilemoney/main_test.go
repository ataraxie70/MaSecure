package main

import (
	"testing"

	"masecure/internal/mobilemoney"
)

func TestBuildProviderCallbackPayloadOrangeMoney(t *testing.T) {
	payload := buildProviderCallbackPayload(
		mobilemoney.PayoutCommand{CycleID: "cycle-001"},
		payoutRecord{
			ExternalRef: "agg-ref-001",
			Provider:    "orange_money",
			Status:      "success",
		},
	)

	if payload["txn_id"] != "agg-ref-001" {
		t.Fatalf("unexpected txn_id: %+v", payload)
	}
	if payload["merchant_ref"] != "cycle-001" {
		t.Fatalf("unexpected merchant_ref: %+v", payload)
	}
	if payload["status"] != "success" {
		t.Fatalf("unexpected status: %+v", payload)
	}
}

func TestBuildProviderCallbackPayloadWaveFailure(t *testing.T) {
	payload := buildProviderCallbackPayload(
		mobilemoney.PayoutCommand{CycleID: "cycle-002"},
		payoutRecord{
			ExternalRef: "agg-ref-002",
			Provider:    "wave",
			Status:      "failed",
		},
	)

	if payload["id"] != "agg-ref-002" {
		t.Fatalf("unexpected id: %+v", payload)
	}
	if payload["client_reference"] != "cycle-002" {
		t.Fatalf("unexpected client_reference: %+v", payload)
	}
	if payload["event_status"] != "failed" {
		t.Fatalf("unexpected event_status: %+v", payload)
	}
	if payload["reason"] != "simulated_provider_failure" {
		t.Fatalf("unexpected reason: %+v", payload)
	}
}
