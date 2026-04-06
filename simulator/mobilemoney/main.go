package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"masecure/internal/mobilemoney"
)

type payoutRecord struct {
	ExternalRef string
	Provider    string
	Status      string
}

type payoutSimulator struct {
	apiKey         string
	requestSecret  string
	callbackSecret string
	callbackBase   string
	mode           string
	delay          time.Duration
	client         *http.Client
	log            *zap.Logger

	mu      sync.Mutex
	records map[string]payoutRecord
}

func main() {
	log := buildLogger()
	defer log.Sync()

	sim := &payoutSimulator{
		apiKey:         mustEnv("MOBILE_MONEY_API_KEY"),
		requestSecret:  getEnv("MOBILE_MONEY_HMAC_SECRET", ""),
		callbackSecret: getEnv("CALLBACK_HMAC_SECRET", ""),
		callbackBase:   getEnv("MOBILE_MONEY_CALLBACK_BASE_URL", "http://127.0.0.1:8000"),
		mode:           strings.ToLower(getEnv("MOBILE_MONEY_SIMULATOR_MODE", "success")),
		delay:          time.Duration(getEnvInt("MOBILE_MONEY_SIMULATOR_DELAY_MS", 250)) * time.Millisecond,
		client:         &http.Client{Timeout: 5 * time.Second},
		log:            log,
		records:        make(map[string]payoutRecord),
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "masecure-mobile-money-simulator",
			"mode":    sim.mode,
		})
	})
	r.Post("/payouts", sim.handlePayout)

	addr := getEnv("MOBILE_MONEY_SIMULATOR_LISTEN_ADDR", "0.0.0.0:18002")
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("Mobile Money simulator listening", zap.String("addr", addr), zap.String("mode", sim.mode))
		serverErr <- srv.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-serverErr:
		log.Fatal("Simulator server error", zap.Error(err))
	case sig := <-quit:
		log.Info("Shutdown signal received", zap.String("signal", sig.String()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("Simulator graceful shutdown failed", zap.Error(err))
	}
}

func (s *payoutSimulator) handlePayout(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if r.Header.Get("X-API-Key") != s.apiKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifySignature(r.Header.Get("X-Signature"), body) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req mobilemoney.AggregatorPayoutRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Payout.IdempotencyKey) == "" || strings.TrimSpace(req.Payout.CycleID) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	provider := mobilemoney.NormalizeProvider(req.Provider)
	status := s.callbackStatus()

	record, firstDelivery := s.recordPayout(req.Payout.IdempotencyKey, provider, status)
	if firstDelivery {
		go s.dispatchCallback(req.Payout, record)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(mobilemoney.AggregatorPayoutResponse{
		ExternalRef: record.ExternalRef,
		Status:      "accepted",
	})
}

func (s *payoutSimulator) recordPayout(
	idempotencyKey string,
	provider string,
	status string,
) (payoutRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.records[idempotencyKey]; ok {
		return existing, false
	}

	record := payoutRecord{
		ExternalRef: "agg-ref-" + idempotencyKey,
		Provider:    provider,
		Status:      status,
	}
	s.records[idempotencyKey] = record
	return record, true
}

func (s *payoutSimulator) dispatchCallback(cmd mobilemoney.PayoutCommand, record payoutRecord) {
	time.Sleep(s.delay)

	body, err := json.Marshal(buildProviderCallbackPayload(cmd, record))
	if err != nil {
		s.log.Error("Failed to marshal simulator callback payload", zap.Error(err))
		return
	}

	url := strings.TrimRight(s.callbackBase, "/") + "/callbacks/mobile-money/payouts/" + record.Provider
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		url,
		bytes.NewReader(body),
	)
	if err != nil {
		s.log.Error("Failed to build simulator callback request", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.callbackSecret != "" {
		req.Header.Set("X-Signature", "sha256="+sign(body, s.callbackSecret))
	}

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Error("Simulator callback delivery failed", zap.Error(err), zap.String("url", url))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		s.log.Error("Simulator callback rejected by API",
			zap.Int("status", resp.StatusCode),
			zap.String("body", strings.TrimSpace(string(raw))),
			zap.String("url", url),
		)
		return
	}

	s.log.Info("Simulator callback delivered",
		zap.String("cycle_id", cmd.CycleID),
		zap.String("external_ref", record.ExternalRef),
		zap.String("provider", record.Provider),
		zap.String("status", record.Status),
	)
}

func buildProviderCallbackPayload(
	cmd mobilemoney.PayoutCommand,
	record payoutRecord,
) map[string]string {
	reason := ""
	if record.Status == "failed" {
		reason = "simulated_provider_failure"
	}

	switch record.Provider {
	case "orange_money":
		payload := map[string]string{
			"txn_id":       record.ExternalRef,
			"merchant_ref": cmd.CycleID,
			"status":       record.Status,
		}
		if reason != "" {
			payload["failure_reason"] = reason
		}
		return payload
	case "moov_money":
		payload := map[string]string{
			"payment_reference": record.ExternalRef,
			"external_ref":      cmd.CycleID,
			"result":            record.Status,
		}
		if reason != "" {
			payload["reason"] = reason
		}
		return payload
	case "wave":
		payload := map[string]string{
			"id":               record.ExternalRef,
			"client_reference": cmd.CycleID,
			"event_status":     record.Status,
		}
		if reason != "" {
			payload["reason"] = reason
		}
		return payload
	default:
		payload := map[string]string{
			"cycle_id":     cmd.CycleID,
			"external_ref": record.ExternalRef,
			"status":       record.Status,
			"provider":     record.Provider,
		}
		if reason != "" {
			payload["reason"] = reason
		}
		return payload
	}
}

func (s *payoutSimulator) verifySignature(signature string, body []byte) bool {
	if s.requestSecret == "" {
		return true
	}
	expected := "sha256=" + sign(body, s.requestSecret)
	return hmac.Equal([]byte(signature), []byte(expected))
}

func (s *payoutSimulator) callbackStatus() string {
	if s.mode == "failed" {
		return "failed"
	}
	return "success"
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic("required env var missing: " + key)
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(raw, "%d", &parsed); err != nil {
		return fallback
	}
	return parsed
}

func buildLogger() *zap.Logger {
	level := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	}
	cfg := zap.Config{
		Level:            level,
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}
	log, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	return log
}
