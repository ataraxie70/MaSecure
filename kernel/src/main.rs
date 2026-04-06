//! MaSecure Kernel — Point d'entrée principal (Phase 1-4)
mod application;
mod domain;
mod http_server;
mod infrastructure;
mod ports;

use anyhow::Context;
use std::sync::Arc;
use std::time::Duration;
use tracing::info;
use tracing_subscriber::EnvFilter;

use application::contribution_service::ContributionService;
use application::payout_service::PayoutService;
use application::resilience_service::ResilienceService;
use http_server::{run_http_server, KernelHttpState};
use infrastructure::{
    pg_contribution_repo::PgContributionRepository,
    pg_cycle_repo::PgCycleRepository,
    pg_debt_repo::PgDebtRepository,
    pg_identity_repo::PgIdentityRepository,
    pg_ledger_repo::PgLedgerRepository,
    pg_outbox_repo::PgOutboxRepository,
    pg_working_capital_repo::PgWorkingCapitalRepository,
    resilience_scheduler::ResilienceScheduler,
    scheduler::PayoutScheduler,
};

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .init();

    info!("MaSecure Kernel v{} starting", env!("CARGO_PKG_VERSION"));

    let database_url = std::env::var("DATABASE_URL").context("DATABASE_URL required")?;
    let service_name = std::env::var("KERNEL_SERVICE_NAME")
        .unwrap_or_else(|_| format!("kernel-financial@{}", env!("CARGO_PKG_VERSION")));
    let scheduler_interval = std::env::var("PAYOUT_SCHEDULER_INTERVAL_SECONDS")
        .ok().and_then(|s| s.parse::<u64>().ok()).unwrap_or(60);
    let resilience_interval = std::env::var("RESILIENCE_SCHEDULER_INTERVAL_SECONDS")
        .ok().and_then(|s| s.parse::<u64>().ok()).unwrap_or(300);

    let pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(20)
        .acquire_timeout(Duration::from_secs(5))
        .connect(&database_url)
        .await
        .context("Failed to connect to PostgreSQL")?;
    info!("PostgreSQL pool established");

    let ledger_repo       = Arc::new(PgLedgerRepository::new(pool.clone()));
    let cycle_repo        = Arc::new(PgCycleRepository::new(pool.clone()));
    let outbox_repo       = Arc::new(PgOutboxRepository::new(pool.clone()));
    let identity_repo     = Arc::new(PgIdentityRepository::new(pool.clone()));
    let contribution_repo = Arc::new(PgContributionRepository::new(pool.clone()));
    let wc_repo           = Arc::new(PgWorkingCapitalRepository::new(pool.clone()));
    let debt_repo         = Arc::new(PgDebtRepository::new(pool.clone()));

    let payout_service = Arc::new(PayoutService {
        cycle_repo:   cycle_repo.clone(),
        ledger_repo:  ledger_repo.clone(),
        outbox_repo:  outbox_repo.clone(),
        db:           pool.clone(),
        service_name: service_name.clone(),
    });

    let contribution_service = Arc::new(ContributionService {
        identity_repo,
        contribution_repo,
        ledger_repo:  ledger_repo.clone(),
        outbox_repo:  outbox_repo.clone(),
        db:           pool.clone(),
        service_name: service_name.clone(),
    });

    let resilience_service = Arc::new(ResilienceService {
        cycle_repo:   cycle_repo.clone(),
        ledger_repo:  ledger_repo.clone(),
        outbox_repo:  outbox_repo.clone(),
        wc_repo,
        debt_repo,
        db:           pool.clone(),
        service_name: service_name.clone(),
    });

    let (payout_tx,     payout_rx)     = tokio::sync::oneshot::channel::<()>();
    let (resilience_tx, resilience_rx) = tokio::sync::oneshot::channel::<()>();

    let payout_handle = tokio::spawn({
        let sched = PayoutScheduler::new(Arc::clone(&payout_service), scheduler_interval);
        async move { sched.run(payout_rx).await }
    });

    let resilience_handle = tokio::spawn({
        let sched = ResilienceScheduler::new(Arc::clone(&resilience_service), resilience_interval);
        async move { sched.run(resilience_rx).await }
    });

    let kernel_http_addr = std::env::var("KERNEL_HTTP_ADDR")
        .unwrap_or_else(|_| "0.0.0.0:8001".into());
    let http_state = KernelHttpState { contribution_service, payout_service };
    tokio::spawn(async move {
        if let Err(err) = run_http_server(http_state, kernel_http_addr).await {
            tracing::error!("Kernel HTTP error: {}", err);
        }
    });

    tokio::signal::ctrl_c().await.context("Failed to listen for ctrl_c")?;
    info!("Shutdown signal — stopping...");
    let _ = payout_tx.send(());
    let _ = resilience_tx.send(());
    let _ = tokio::time::timeout(Duration::from_secs(10), payout_handle).await;
    let _ = tokio::time::timeout(Duration::from_secs(5), resilience_handle).await;
    info!("Kernel stopped cleanly");
    Ok(())
}
