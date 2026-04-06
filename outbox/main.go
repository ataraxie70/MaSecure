// Package main — Point d'entrée du service Outbox Worker
//
// Ce binaire est déployé séparément du kernel Rust.
// Il poll la table outbox_events et livre les événements en attente
// vers leurs services cibles (Mobile Money gateway, Notification service).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"masecure/internal/mobilemoney"
	"masecure/internal/outboxworker"
)

func main() {
	log := buildLogger()
	defer log.Sync()

	log.Info("MaSecure Outbox Worker starting")

	// ── Configuration ─────────────────────────────────────────────────────────
	dbURL := mustEnv("DATABASE_URL")
	mmMode := getEnv("MOBILE_MONEY_MODE", "mock")
	mmConfig := mobilemoney.LiveConfig{
		BaseURL:    getEnv("MOBILE_MONEY_AGGREGATOR_URL", ""),
		APIKey:     getEnv("MOBILE_MONEY_API_KEY", ""),
		HMACSecret: getEnv("MOBILE_MONEY_HMAC_SECRET", ""),
	}
	kernelURL := getEnv("KERNEL_INTERNAL_URL", "http://127.0.0.1:8001")

	// ── Connexion PostgreSQL ──────────────────────────────────────────────────
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal("Failed to connect to PostgreSQL", zap.Error(err))
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatal("PostgreSQL ping failed", zap.Error(err))
	}
	log.Info("PostgreSQL connection established")

	// ── Dispatchers ───────────────────────────────────────────────────────────
	dispatchers := []outboxworker.Dispatcher{
		NewMobileMoneyDispatcher(newMobileMoneyRegistry(mmMode, mmConfig), kernelURL, log),
		NewNotificationDispatcher(log),
	}

	// ── Worker ────────────────────────────────────────────────────────────────
	worker := outboxworker.NewWorker(pool, dispatchers, log)

	// ── Arrêt gracieux ────────────────────────────────────────────────────────
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-sigCh
		log.Info("Shutdown signal received", zap.String("signal", sig.String()))
		cancel()
	}()

	worker.Run(workerCtx)
	log.Info("Outbox Worker stopped cleanly")
}

// ── Dispatchers concrets ──────────────────────────────────────────────────────

// MobileMoneyDispatcher envoie les PayoutCommand vers l'agrégateur Mobile Money.
type MobileMoneyDispatcher struct {
	registry  *mobilemoney.Registry
	kernelURL string
	client    *http.Client
	log       *zap.Logger
}

func NewMobileMoneyDispatcher(
	registry *mobilemoney.Registry,
	kernelURL string,
	log *zap.Logger,
) *MobileMoneyDispatcher {
	return &MobileMoneyDispatcher{
		registry:  registry,
		kernelURL: strings.TrimRight(kernelURL, "/"),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		log: log,
	}
}

func (d *MobileMoneyDispatcher) CanHandle(target string) bool {
	return target == "mobile-money-gw"
}

func (d *MobileMoneyDispatcher) Dispatch(ctx context.Context, row outboxworker.OutboxRow) (string, error) {
	d.log.Info("Dispatching to Mobile Money",
		zap.String("event_type", row.EventType),
		zap.String("idempotency_key", row.IdempotencyKey),
	)

	var payload payoutTriggeredEvent
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return "", fmt.Errorf("parse payout payload: %w", err)
	}
	if payload.Command.CycleID == "" {
		return "", fmt.Errorf("missing cycle_id in payout payload")
	}
	provider, err := d.registry.Resolve(payload.Command.BeneficiaryProvider)
	if err != nil {
		return "", err
	}

	d.log.Info("Routing payout to Mobile Money provider",
		zap.String("provider", provider.Name()),
		zap.String("cycle_id", payload.Command.CycleID),
	)

	result, err := provider.SendPayout(ctx, payload.Command)
	if err != nil {
		return "", err
	}

	if result.ConfirmImmediately {
		if err := d.confirmPayout(ctx, payload.Command.CycleID, result.ExternalRef); err != nil {
			return "", err
		}
	}
	return result.ExternalRef, nil
}

// NotificationDispatcher envoie les notifications via WhatsApp/SMS.
type NotificationDispatcher struct {
	log *zap.Logger
}

func NewNotificationDispatcher(log *zap.Logger) *NotificationDispatcher {
	return &NotificationDispatcher{log: log}
}

func (d *NotificationDispatcher) CanHandle(target string) bool {
	return target == "notification-svc"
}

func (d *NotificationDispatcher) Dispatch(ctx context.Context, row outboxworker.OutboxRow) (string, error) {
	d.log.Info("Dispatching notification",
		zap.String("event_type", row.EventType),
		zap.String("aggregate_id", row.AggregateID.String()),
	)
	// Dans l'implémentation complète :
	// - PayoutConfirmed   → WhatsApp template "payout_confirmed" au bénéficiaire
	// - ContributionQuarantined → Alerte groupe + bénéficiaire
	// - PayoutFailed      → Alerte urgence groupe + administrateur
	return "notif-ok", nil
}

type payoutTriggeredEvent struct {
	Type    string                    `json:"type"`
	Command mobilemoney.PayoutCommand `json:"command"`
}

func (d *MobileMoneyDispatcher) confirmPayout(ctx context.Context, cycleID, externalRef string) error {
	body, err := json.Marshal(map[string]string{
		"cycle_id":     cycleID,
		"external_ref": externalRef,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		d.kernelURL+"/internal/payouts/confirmations",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("confirm payout via kernel: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("kernel confirmation failed: %s", strings.TrimSpace(string(raw)))
	}

	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "FATAL: environment variable %s is required\n", key)
		os.Exit(1)
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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

func newMobileMoneyRegistry(mode string, config mobilemoney.LiveConfig) *mobilemoney.Registry {
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	switch normalizedMode {
	case "", "mock":
		return mobilemoney.NewRegistry(
			mobilemoney.NewMockProvider("orange_money", 200*time.Millisecond),
			mobilemoney.NewMockProvider("moov_money", 200*time.Millisecond),
			mobilemoney.NewMockProvider("wave", 200*time.Millisecond),
		)
	case "live":
		return mobilemoney.NewRegistry(
			mobilemoney.NewLiveHTTPProvider("orange_money", config),
			mobilemoney.NewLiveHTTPProvider("moov_money", config),
			mobilemoney.NewLiveHTTPProvider("wave", config),
		)
	default:
		panic(fmt.Sprintf("unsupported MOBILE_MONEY_MODE %q", mode))
	}
}
