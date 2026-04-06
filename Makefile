# =============================================================================
# MaSecure — Makefile
# =============================================================================
.PHONY: up down migrate kernel-build kernel-test outbox-run api-run simulator-run \
        verify-ledger lint-rust lint-go clean help

# ── Environnement ─────────────────────────────────────────────────────────────
include .env
export

# ── Infrastructure ─────────────────────────────────────────────────────────────
up:
	@echo "→ Démarrage de l'environnement de développement..."
	docker compose up -d
	@echo "→ En attente que PostgreSQL soit prêt..."
	@until docker compose exec postgres pg_isready -U masecure -q; do sleep 1; done
	@echo "✓ PostgreSQL prêt"

down:
	docker compose down

migrate: up
	@echo "→ Application des migrations SQL..."
	@for f in migrations/*.sql; do \
		echo "  Applying $$f..."; \
		docker compose exec -T postgres psql -U masecure -d masecure_dev < $$f; \
	done
	@echo "✓ Migrations appliquées"

# ── Kernel Rust ────────────────────────────────────────────────────────────────
kernel-build:
	@echo "→ Compilation du kernel Rust..."
	cd kernel && cargo build --release
	@echo "✓ Kernel compilé : kernel/target/release/kernel-server"

kernel-test:
	@echo "→ Tests du kernel Rust..."
	cd kernel && cargo test -- --nocapture
	@echo "✓ Tests kernel passés"

kernel-run: kernel-build
	@echo "→ Démarrage du kernel..."
	cd kernel && RUST_LOG=info ./target/release/kernel-server

verify-ledger: kernel-build
	@echo "→ Vérification de l'intégrité du ledger..."
	cd kernel && ./target/release/verify-ledger

# ── Services Go ───────────────────────────────────────────────────────────────
api-run:
	@echo "→ Démarrage de l'API Go..."
	go run ./api

outbox-run:
	@echo "→ Démarrage de l'Outbox Worker..."
	go run ./outbox

simulator-run:
	@echo "→ Démarrage du simulateur Mobile Money..."
	go run ./simulator/mobilemoney

# ── Qualité ────────────────────────────────────────────────────────────────────
lint-rust:
	cd kernel && cargo clippy -- -D warnings
	cd kernel && cargo fmt --check

lint-go:
	go vet ./...
	@which golangci-lint > /dev/null && golangci-lint run ./... || echo "golangci-lint non installé"

# ── Développement complet ─────────────────────────────────────────────────────
dev: up migrate
	@echo "✓ Environnement prêt"
	@echo ""
	@echo "Démarrer les services dans des terminaux séparés :"
	@echo "  Terminal 1 : make kernel-run"
	@echo "  Terminal 2 : make outbox-run"
	@echo "  Terminal 3 : make api-run"
	@echo "  Terminal 4 : make simulator-run"
	@echo ""
	@echo "Interfaces :"
	@echo "  Adminer (BDD) : http://localhost:8080"
	@echo "  NATS monitor  : http://localhost:8222"
	@echo "  API health    : http://localhost:8000/health"
	@echo "  Kernel health : http://localhost:8001/health"

clean:
	cd kernel && cargo clean
	docker compose down -v

help:
	@echo "Commandes disponibles :"
	@grep -E '^[a-zA-Z_-]+:.*' Makefile | grep -v '^\.' | awk -F: '{print "  make " $$1}'

# ── Phase 3 : Service Social ───────────────────────────────────────────────────
social-run:
	@echo "→ Démarrage du Social Service (Phase 3+)..."
	go run ./social

social-test:
	@echo "→ Tests du Service Social..."
	go test ./social/... -v

# ── Phase 4 : Résilience ──────────────────────────────────────────────────────
kernel-run-phase4: kernel-build
	@echo "→ Démarrage du kernel (PayoutScheduler + ResilienceScheduler)..."
	RESILIENCE_SCHEDULER_INTERVAL_SECONDS=60 \
	cd kernel && RUST_LOG=info ./target/release/kernel-server

# ── Phase 5 : Monitoring ──────────────────────────────────────────────────────
ops-health:
	@echo "→ État opérationnel du système..."
	curl -s http://localhost:8002/internal/ops/health | python3 -m json.tool

anomaly-scan:
	@echo "→ Déclenchement manuel du scan d'anomalies..."
	curl -s -X POST http://localhost:8002/internal/monitoring/scan | python3 -m json.tool

# ── Migrations ────────────────────────────────────────────────────────────────
migrate-phase3:
	@echo "→ Migration Phase 3 : Gouvernance..."
	docker compose exec -T postgres psql -U masecure -d masecure_dev < migrations/008_proposals.sql
	@echo "✓ Migration 008 appliquée"

migrate-phase4:
	@echo "→ Migration Phase 4 : Résilience..."
	docker compose exec -T postgres psql -U masecure -d masecure_dev < migrations/009_resilience.sql
	@echo "✓ Migration 009 appliquée"

migrate-phase5:
	@echo "→ Migration Phase 5 : Conformité & Monitoring..."
	docker compose exec -T postgres psql -U masecure -d masecure_dev < migrations/010_compliance_monitoring.sql
	@echo "✓ Migration 010 appliquée"

migrate-all: migrate migrate-phase3 migrate-phase4 migrate-phase5
	@echo "✓ Toutes les migrations appliquées (001→010)"

# ── Dev complet Phases 1-5 ────────────────────────────────────────────────────
dev-full: up migrate-all
	@echo "✓ Environnement complet prêt"
	@echo ""
	@echo "Démarrer les services dans des terminaux séparés :"
	@echo "  Terminal 1 : make kernel-run           # Kernel Rust (payout + resilience)"
	@echo "  Terminal 2 : make outbox-run           # Outbox Worker + Notifications réelles"
	@echo "  Terminal 3 : make api-run              # API Gateway Go"
	@echo "  Terminal 4 : make social-run           # Social Service (gouvernance + audit)"
	@echo "  Terminal 5 : make simulator-run        # Simulateur MM (Phase 2 locale)"
	@echo ""
	@echo "Interfaces :"
	@echo "  Adminer     : http://localhost:8080"
	@echo "  NATS        : http://localhost:8222"
	@echo "  API health  : http://localhost:8000/health"
	@echo "  Kernel      : http://localhost:8001/health"
	@echo "  Social      : http://localhost:8002/health"
	@echo "  Ops health  : http://localhost:8002/internal/ops/health"

# ── Tests complets ────────────────────────────────────────────────────────────
test-all: kernel-test
	go test ./... -count=1
	@echo "✓ Tous les tests passés"

# ── Audit d'un groupe ─────────────────────────────────────────────────────────
audit-group:
ifndef GROUP_ID
	$(error GROUP_ID is required: make audit-group GROUP_ID=<uuid>)
endif
	curl -s "http://localhost:8002/groups/$(GROUP_ID)/audit" | python3 -m json.tool

# ── Dashboard groupe ──────────────────────────────────────────────────────────
dashboard-group:
ifndef GROUP_ID
	$(error GROUP_ID is required: make dashboard-group GROUP_ID=<uuid>)
endif
	curl -s "http://localhost:8002/groups/$(GROUP_ID)/dashboard" | python3 -m json.tool
