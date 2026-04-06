// Package main — Point d'entrée du service API MaSecure
//
// Expose les endpoints HTTP :
//   POST /callbacks/mobile-money  — callbacks opérateurs (HMAC vérifié)
//   POST /webhooks/whatsapp       — messages entrants WhatsApp (délégué à la gateway)
//   GET  /health                  — statut du service

package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"masecure/api/handlers"
	"masecure/gateway/whatsapp"
)

func main() {
	log := buildLogger()
	defer log.Sync()

	log.Info("MaSecure API starting")

	dbURL := mustEnv("DATABASE_URL")
	hmacSecret := getEnv("CALLBACK_HMAC_SECRET", "")
	waSecret := getEnv("WHATSAPP_APP_SECRET", "")
	waToken := getEnv("WHATSAPP_VERIFY_TOKEN", "masecure-dev-token")
	kernelURL := getEnv("KERNEL_INTERNAL_URL", "http://127.0.0.1:8001")
	listenAddr := getEnv("API_LISTEN_ADDR", "0.0.0.0:8000")

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal("PostgreSQL connection failed", zap.Error(err))
	}
	defer pool.Close()
	log.Info("PostgreSQL connected")

	// ── Canal d'intentions WhatsApp ────────────────────────────────────────────
	// Les UserIntent sont envoyés vers le Service Social via ce canal.
	intentCh := make(chan whatsapp.UserIntent, 256)

	// Consumer des intentions (dans un vrai système : envoi vers NATS)
	go consumeIntents(intentCh, log)

	// ── Handlers ──────────────────────────────────────────────────────────────
	kernelClient := handlers.NewHTTPKernelClient(kernelURL)
	callbackHandler := handlers.NewCallbackHandler(hmacSecret, kernelClient, log)
	payoutCallbackHandler := handlers.NewPayoutCallbackHandler(hmacSecret, kernelClient, log)
	waHandler := whatsapp.NewWebhookHandler(waSecret, waToken, intentCh, log)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(zapMiddleware(log))
	r.Use(middleware.Recoverer)

	// Health check — ne révèle aucune information sensible
	r.Get("/health", handlers.HealthHandler)

	// Callbacks Mobile Money (HMAC vérifié)
	r.Post("/callbacks/mobile-money", callbackHandler.HandleMobileMoneyCallback)
	r.Post("/callbacks/mobile-money/{provider}", callbackHandler.HandleMobileMoneyCallback)
	r.Post("/callbacks/mobile-money/payouts", payoutCallbackHandler.HandlePayoutStatusCallback)
	r.Post(
		"/callbacks/mobile-money/payouts/{provider}",
		payoutCallbackHandler.HandlePayoutStatusCallback,
	)

	// Webhook WhatsApp (HMAC Meta vérifié)
	r.Get("/webhooks/whatsapp", waHandler.HandleVerification) // vérification Meta
	r.Post("/webhooks/whatsapp", waHandler.HandleEvent)       // messages entrants

	// ── Serveur HTTP ──────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("API server listening", zap.String("addr", listenAddr))
		serverErr <- srv.ListenAndServe()
	}()

	// ── Arrêt gracieux ────────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-serverErr:
		log.Fatal("Server error", zap.Error(err))
	case sig := <-quit:
		log.Info("Shutdown signal received", zap.String("signal", sig.String()))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("Graceful shutdown failed", zap.Error(err))
	}
	log.Info("API server stopped cleanly")
}

// consumeIntents traite les UserIntent reçus depuis la gateway WhatsApp.
// Dans le système complet, ces intentions sont publiées sur NATS
// et consommées par le Service Social.
func consumeIntents(ch <-chan whatsapp.UserIntent, log *zap.Logger) {
	for intent := range ch {
		log.Info("UserIntent received",
			zap.String("type", string(intent.Type)),
			zap.String("from", intent.FromMsisdn),
			zap.String("group", intent.GroupRef),
		)
		// Routing vers le Service Social selon le type d'intention :
		// IntentVoteApprove/Reject → config_proposals vote update
		// IntentQueryBalance       → cycle_collection_summary query
		// IntentConfirmParticipation → group_members status update
		// IntentQueryAudit         → ledger_entries export
	}
}

func zapMiddleware(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("HTTP request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Duration("latency", time.Since(start)),
			)
		})
	}
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

func buildLogger() *zap.Logger {
	level := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	}
	cfg := zap.Config{
		Level:            level,
		Encoding:         "json",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}
	l, _ := cfg.Build()
	return l
}
