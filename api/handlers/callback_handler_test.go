package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

type mockKernelClient struct {
	calls                 int
	payloads              []InternalContributionPayload
	payoutConfirmCalls    int
	payoutFailureCalls    int
	payoutConfirmPayloads []InternalPayoutConfirmationPayload
	payoutFailurePayloads []InternalPayoutFailurePayload
	err                   error
	done                  chan struct{}
}

func (m *mockKernelClient) ForwardContributionCallback(
	_ context.Context,
	payload InternalContributionPayload,
) error {
	m.calls++
	m.payloads = append(m.payloads, payload)
	if m.done != nil {
		select {
		case m.done <- struct{}{}:
		default:
		}
	}
	return m.err
}

func (m *mockKernelClient) ForwardPayoutConfirmation(
	_ context.Context,
	payload InternalPayoutConfirmationPayload,
) error {
	m.payoutConfirmCalls++
	m.payoutConfirmPayloads = append(m.payoutConfirmPayloads, payload)
	if m.done != nil {
		select {
		case m.done <- struct{}{}:
		default:
		}
	}
	return m.err
}

func (m *mockKernelClient) ForwardPayoutFailure(
	_ context.Context,
	payload InternalPayoutFailurePayload,
) error {
	m.payoutFailureCalls++
	m.payoutFailurePayloads = append(m.payoutFailurePayloads, payload)
	if m.done != nil {
		select {
		case m.done <- struct{}{}:
		default:
		}
	}
	return m.err
}

func TestProcessCallbackForwardsSuccessfulContribution(t *testing.T) {
	mock := &mockKernelClient{}
	handler := NewCallbackHandler("", mock, zap.NewNop())

	handler.processCallback(MobileMoneyCallbackPayload{
		ProviderTxRef: "txn-123",
		PayerMsisdn:   "+22670000000",
		AmountMinor:   15000,
		Status:        "success",
		CycleRef:      "0b09c9cf-d44a-42a4-90dd-83b1f4c94d8d",
		Provider:      "orange_money",
	})

	if mock.calls != 1 {
		t.Fatalf("expected one forwarded callback, got %d", mock.calls)
	}
	got := mock.payloads[0]
	if got.ProviderTxRef != "txn-123" || got.CycleID != "0b09c9cf-d44a-42a4-90dd-83b1f4c94d8d" {
		t.Fatalf("unexpected forwarded payload: %+v", got)
	}
}

func TestProcessCallbackIgnoresNonSuccessStatuses(t *testing.T) {
	mock := &mockKernelClient{}
	handler := NewCallbackHandler("", mock, zap.NewNop())

	handler.processCallback(MobileMoneyCallbackPayload{
		ProviderTxRef: "txn-ignored",
		Status:        "failed",
	})

	if mock.calls != 0 {
		t.Fatalf("expected no forwarded callback, got %d", mock.calls)
	}
}

func TestHandleMobileMoneyCallbackParsesProviderRoute(t *testing.T) {
	mock := &mockKernelClient{done: make(chan struct{}, 1)}
	handler := NewCallbackHandler("", mock, zap.NewNop())

	req := httptest.NewRequest(
		http.MethodPost,
		"/callbacks/mobile-money/orange-money",
		strings.NewReader(`{
			"txn_id":"orange-002",
			"subscriber_msisdn":"+22670000012",
			"amount_minor":12000,
			"status":"SUCCESS",
			"merchant_ref":"cycle-002"
		}`),
	)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("provider", "orange-money")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	handler.HandleMobileMoneyCallback(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	select {
	case <-mock.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for callback forwarding")
	}

	if mock.calls != 1 {
		t.Fatalf("expected one forwarded callback, got %d", mock.calls)
	}
	if got := mock.payloads[0].Provider; got != "orange_money" {
		t.Fatalf("unexpected provider %s", got)
	}
}

func TestHandleMobileMoneyCallbackRejectsUnknownProviderRoute(t *testing.T) {
	mock := &mockKernelClient{done: make(chan struct{}, 1)}
	handler := NewCallbackHandler("", mock, zap.NewNop())

	req := httptest.NewRequest(
		http.MethodPost,
		"/callbacks/mobile-money/unknown",
		strings.NewReader(`{"hello":"world"}`),
	)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("provider", "unknown")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	handler.HandleMobileMoneyCallback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if mock.calls != 0 {
		t.Fatalf("expected no forwarded callback, got %d", mock.calls)
	}
}
