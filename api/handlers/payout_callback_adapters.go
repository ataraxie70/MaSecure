package handlers

import (
	"encoding/json"
	"fmt"
	"strings"

	"masecure/internal/mobilemoney"
)

type PayoutCallbackAdapter interface {
	Parse(body []byte) (MobileMoneyPayoutStatusPayload, error)
}

type PayoutCallbackAdapterRegistry struct {
	defaultAdapter PayoutCallbackAdapter
	adapters       map[string]PayoutCallbackAdapter
}

func NewPayoutCallbackAdapterRegistry() *PayoutCallbackAdapterRegistry {
	return &PayoutCallbackAdapterRegistry{
		defaultAdapter: genericPayoutCallbackAdapter{},
		adapters: map[string]PayoutCallbackAdapter{
			"orange_money": orangeMoneyPayoutCallbackAdapter{},
			"moov_money":   moovMoneyPayoutCallbackAdapter{},
			"wave":         wavePayoutCallbackAdapter{},
		},
	}
}

func (r *PayoutCallbackAdapterRegistry) Resolve(provider string) (PayoutCallbackAdapter, error) {
	if strings.TrimSpace(provider) == "" {
		return r.defaultAdapter, nil
	}

	normalized := mobilemoney.NormalizeProvider(provider)
	adapter, ok := r.adapters[normalized]
	if !ok {
		return nil, fmt.Errorf("unsupported payout callback provider %q", provider)
	}
	return adapter, nil
}

type genericPayoutCallbackAdapter struct{}

func (genericPayoutCallbackAdapter) Parse(body []byte) (MobileMoneyPayoutStatusPayload, error) {
	var payload MobileMoneyPayoutStatusPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return MobileMoneyPayoutStatusPayload{}, err
	}

	payload.Provider = normalizeIncomingProvider(payload.Provider)
	payload.Status = normalizeIncomingStatus(payload.Status)
	if err := payload.validate(); err != nil {
		return MobileMoneyPayoutStatusPayload{}, err
	}
	return payload, nil
}

type orangeMoneyPayoutCallbackAdapter struct{}

type orangeMoneyPayoutStatusPayload struct {
	TransactionID string `json:"txn_id"`
	CycleID       string `json:"merchant_ref"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
}

func (orangeMoneyPayoutCallbackAdapter) Parse(body []byte) (MobileMoneyPayoutStatusPayload, error) {
	var payload orangeMoneyPayoutStatusPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return MobileMoneyPayoutStatusPayload{}, err
	}

	normalized := MobileMoneyPayoutStatusPayload{
		CycleID:     payload.CycleID,
		ExternalRef: payload.TransactionID,
		Status:      normalizeIncomingStatus(payload.Status),
		Provider:    "orange_money",
		Reason:      payload.FailureReason,
	}
	if err := normalized.validate(); err != nil {
		return MobileMoneyPayoutStatusPayload{}, err
	}
	return normalized, nil
}

type moovMoneyPayoutCallbackAdapter struct{}

type moovMoneyPayoutStatusPayload struct {
	PaymentReference string `json:"payment_reference"`
	CycleID          string `json:"external_ref"`
	Result           string `json:"result"`
	Reason           string `json:"reason"`
}

func (moovMoneyPayoutCallbackAdapter) Parse(body []byte) (MobileMoneyPayoutStatusPayload, error) {
	var payload moovMoneyPayoutStatusPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return MobileMoneyPayoutStatusPayload{}, err
	}

	normalized := MobileMoneyPayoutStatusPayload{
		CycleID:     payload.CycleID,
		ExternalRef: payload.PaymentReference,
		Status:      normalizeIncomingStatus(payload.Result),
		Provider:    "moov_money",
		Reason:      payload.Reason,
	}
	if err := normalized.validate(); err != nil {
		return MobileMoneyPayoutStatusPayload{}, err
	}
	return normalized, nil
}

type wavePayoutCallbackAdapter struct{}

type wavePayoutStatusPayload struct {
	TransactionID string `json:"id"`
	CycleID       string `json:"client_reference"`
	Status        string `json:"event_status"`
	Reason        string `json:"reason"`
}

func (wavePayoutCallbackAdapter) Parse(body []byte) (MobileMoneyPayoutStatusPayload, error) {
	var payload wavePayoutStatusPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return MobileMoneyPayoutStatusPayload{}, err
	}

	normalized := MobileMoneyPayoutStatusPayload{
		CycleID:     payload.CycleID,
		ExternalRef: payload.TransactionID,
		Status:      normalizeIncomingStatus(payload.Status),
		Provider:    "wave",
		Reason:      payload.Reason,
	}
	if err := normalized.validate(); err != nil {
		return MobileMoneyPayoutStatusPayload{}, err
	}
	return normalized, nil
}
