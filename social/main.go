package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"masecure/social/governance"
)

func main() {
	log := buildLogger()
	defer log.Sync()

	log.Info("MaSecure Social service starting")

	dbURL := mustEnv("DATABASE_URL")
	listenAddr := getEnv("SOCIAL_LISTEN_ADDR", "0.0.0.0:8002")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal("PostgreSQL connection failed", zap.Error(err))
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatal("PostgreSQL ping failed", zap.Error(err))
	}
	log.Info("PostgreSQL connected")

	repo := governance.NewRepository(pool)
	service := governance.NewService(repo)
	handler := governance.NewHTTPHandler(service, log)

	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      handler.Routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("Social service listening", zap.String("addr", listenAddr))
		serverErr <- srv.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-serverErr:
		log.Fatal("Social server error", zap.Error(err))
	case sig := <-quit:
		log.Info("Shutdown signal received", zap.String("signal", sig.String()))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("Social graceful shutdown failed", zap.Error(err))
	}
	log.Info("Social service stopped cleanly")
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
	log, _ := cfg.Build()
	return log
}
