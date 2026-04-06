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

func TestHandlePayoutStatusCallbackForwardsConfirmation(t *testing.T) {
	mock := &mockKernelClient{done: make(chan struct{}, 1)}
	handler := NewPayoutCallbackHandler("", mock, zap.NewNop())

	req := httptest.NewRequest(
		http.MethodPost,
		"/callbacks/mobile-money/payouts/orange-money",
		strings.NewReader(`{
			"txn_id":"ext-confirm-001",
			"merchant_ref":"cycle-confirm-001",
			"status":"SUCCESS"
		}`),
	)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("provider", "orange-money")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	handler.HandlePayoutStatusCallback(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	select {
	case <-mock.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for payout confirmation forwarding")
	}

	if mock.payoutConfirmCalls != 1 {
		t.Fatalf("expected one payout confirmation, got %d", mock.payoutConfirmCalls)
	}
	if got := mock.payoutConfirmPayloads[0].CycleID; got != "cycle-confirm-001" {
		t.Fatalf("unexpected cycle id %s", got)
	}
}

func TestHandlePayoutStatusCallbackForwardsFailure(t *testing.T) {
	mock := &mockKernelClient{done: make(chan struct{}, 1)}
	handler := NewPayoutCallbackHandler("", mock, zap.NewNop())

	req := httptest.NewRequest(
		http.MethodPost,
		"/callbacks/mobile-money/payouts/wave",
		strings.NewReader(`{
			"id":"ext-failed-001",
			"client_reference":"cycle-failed-001",
			"event_status":"FAILED",
			"reason":"insufficient_balance"
		}`),
	)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("provider", "wave")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	handler.HandlePayoutStatusCallback(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	select {
	case <-mock.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for payout failure forwarding")
	}

	if mock.payoutFailureCalls != 1 {
		t.Fatalf("expected one payout failure, got %d", mock.payoutFailureCalls)
	}
	if got := mock.payoutFailurePayloads[0].Reason; got != "insufficient_balance" {
		t.Fatalf("unexpected failure reason %s", got)
	}
}
