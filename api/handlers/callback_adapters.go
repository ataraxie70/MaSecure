package handlers

import (
	"encoding/json"
	"fmt"
	"strings"

	"masecure/internal/mobilemoney"
)

type CallbackAdapter interface {
	Parse(body []byte) (MobileMoneyCallbackPayload, error)
}

type CallbackAdapterRegistry struct {
	defaultAdapter CallbackAdapter
	adapters       map[string]CallbackAdapter
}

func NewCallbackAdapterRegistry() *CallbackAdapterRegistry {
	return &CallbackAdapterRegistry{
		defaultAdapter: genericCallbackAdapter{},
		adapters: map[string]CallbackAdapter{
			"orange_money": orangeMoneyCallbackAdapter{},
			"moov_money":   moovMoneyCallbackAdapter{},
			"wave":         waveCallbackAdapter{},
		},
	}
}

func (r *CallbackAdapterRegistry) Resolve(provider string) (CallbackAdapter, error) {
	if strings.TrimSpace(provider) == "" {
		return r.defaultAdapter, nil
	}

	normalized := mobilemoney.NormalizeProvider(provider)
	adapter, ok := r.adapters[normalized]
	if !ok {
		return nil, fmt.Errorf("unsupported callback provider %q", provider)
	}
	return adapter, nil
}

type genericCallbackAdapter struct{}

func (genericCallbackAdapter) Parse(body []byte) (MobileMoneyCallbackPayload, error) {
	var payload MobileMoneyCallbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return MobileMoneyCallbackPayload{}, err
	}

	payload.Provider = normalizeIncomingProvider(payload.Provider)
	payload.Status = normalizeIncomingStatus(payload.Status)
	if err := payload.validate(); err != nil {
		return MobileMoneyCallbackPayload{}, err
	}
	return payload, nil
}

type orangeMoneyCallbackAdapter struct{}

type orangeMoneyCallbackPayload struct {
	TransactionID string `json:"txn_id"`
	PayerMsisdn   string `json:"subscriber_msisdn"`
	AmountMinor   int64  `json:"amount_minor"`
	Status        string `json:"status"`
	CycleRef      string `json:"merchant_ref"`
}

func (orangeMoneyCallbackAdapter) Parse(body []byte) (MobileMoneyCallbackPayload, error) {
	var payload orangeMoneyCallbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return MobileMoneyCallbackPayload{}, err
	}

	normalized := MobileMoneyCallbackPayload{
		ProviderTxRef: payload.TransactionID,
		PayerMsisdn:   payload.PayerMsisdn,
		AmountMinor:   payload.AmountMinor,
		Status:        normalizeIncomingStatus(payload.Status),
		CycleRef:      payload.CycleRef,
		Provider:      "orange_money",
	}
	if err := normalized.validate(); err != nil {
		return MobileMoneyCallbackPayload{}, err
	}
	return normalized, nil
}

type moovMoneyCallbackAdapter struct{}

type moovMoneyCallbackPayload struct {
	PaymentReference string `json:"payment_reference"`
	PayerMsisdn      string `json:"payer_msisdn"`
	AmountMinor      int64  `json:"amount_minor"`
	Result           string `json:"result"`
	CycleRef         string `json:"external_ref"`
}

func (moovMoneyCallbackAdapter) Parse(body []byte) (MobileMoneyCallbackPayload, error) {
	var payload moovMoneyCallbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return MobileMoneyCallbackPayload{}, err
	}

	normalized := MobileMoneyCallbackPayload{
		ProviderTxRef: payload.PaymentReference,
		PayerMsisdn:   payload.PayerMsisdn,
		AmountMinor:   payload.AmountMinor,
		Status:        normalizeIncomingStatus(payload.Result),
		CycleRef:      payload.CycleRef,
		Provider:      "moov_money",
	}
	if err := normalized.validate(); err != nil {
		return MobileMoneyCallbackPayload{}, err
	}
	return normalized, nil
}

type waveCallbackAdapter struct{}

type waveCallbackPayload struct {
	TransactionID string `json:"id"`
	PayerMsisdn   string `json:"phone_number"`
	AmountMinor   int64  `json:"amount_minor"`
	Status        string `json:"event_status"`
	CycleRef      string `json:"client_reference"`
}

func (waveCallbackAdapter) Parse(body []byte) (MobileMoneyCallbackPayload, error) {
	var payload waveCallbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return MobileMoneyCallbackPayload{}, err
	}

	normalized := MobileMoneyCallbackPayload{
		ProviderTxRef: payload.TransactionID,
		PayerMsisdn:   payload.PayerMsisdn,
		AmountMinor:   payload.AmountMinor,
		Status:        normalizeIncomingStatus(payload.Status),
		CycleRef:      payload.CycleRef,
		Provider:      "wave",
	}
	if err := normalized.validate(); err != nil {
		return MobileMoneyCallbackPayload{}, err
	}
	return normalized, nil
}

func normalizeIncomingProvider(provider string) string {
	normalized := mobilemoney.NormalizeProvider(provider)
	if normalized == "" {
		return provider
	}
	return normalized
}

func normalizeIncomingStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "successful", "completed", "complete", "paid", "ok":
		return "success"
	case "failed", "failure", "error", "cancelled", "canceled", "rejected":
		return "failed"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}
