use std::sync::Arc;

use anyhow::Context;
use axum::{
    extract::State,
    http::StatusCode,
    response::IntoResponse,
    routing::{get, post},
    Json, Router,
};
use serde::{Deserialize, Serialize};
use time::OffsetDateTime;
use uuid::Uuid;

use crate::application::contribution_service::{
    CallbackOutcome, ContributionService, MobileMoneyCallback,
};
use crate::application::payout_service::{
    PayoutConfirmationOutcome, PayoutFailureOutcome, PayoutService,
};

#[derive(Clone)]
pub struct KernelHttpState {
    pub contribution_service: Arc<ContributionService>,
    pub payout_service: Arc<PayoutService>,
}

#[derive(Debug, Deserialize)]
struct InternalContributionRequest {
    provider_tx_ref: String,
    payer_msisdn: String,
    amount_minor: i64,
    cycle_id: Uuid,
    provider: String,
    timestamp: Option<OffsetDateTime>,
}

#[derive(Debug, Deserialize)]
struct InternalPayoutConfirmationRequest {
    cycle_id: Uuid,
    external_ref: String,
}

#[derive(Debug, Deserialize)]
struct InternalPayoutFailureRequest {
    cycle_id: Uuid,
    external_ref: String,
    reason: String,
}

#[derive(Debug, Serialize)]
struct StatusResponse {
    status: &'static str,
    service: &'static str,
}

pub async fn run_http_server(state: KernelHttpState, addr: String) -> anyhow::Result<()> {
    let app = Router::new()
        .route("/health", get(health))
        .route(
            "/internal/callbacks/mobile-money",
            post(handle_contribution_callback),
        )
        .route(
            "/internal/payouts/confirmations",
            post(handle_payout_confirmation),
        )
        .route("/internal/payouts/failures", post(handle_payout_failure))
        .with_state(state);

    let listener = tokio::net::TcpListener::bind(&addr)
        .await
        .with_context(|| format!("Failed to bind kernel HTTP server on {}", addr))?;

    tracing::info!(addr = %addr, "Kernel HTTP server listening");
    tracing::info!(
        "HTTP routes: GET /health, POST /internal/callbacks/mobile-money, POST /internal/payouts/confirmations, POST /internal/payouts/failures"
    );

    axum::serve(listener, app)
        .await
        .context("Kernel HTTP server exited unexpectedly")?;
    Ok(())
}

async fn health() -> Json<StatusResponse> {
    Json(StatusResponse {
        status: "ok",
        service: "masecure-kernel",
    })
}

async fn handle_contribution_callback(
    State(state): State<KernelHttpState>,
    Json(req): Json<InternalContributionRequest>,
) -> impl IntoResponse {
    let callback = MobileMoneyCallback {
        provider_tx_ref: req.provider_tx_ref,
        payer_msisdn: req.payer_msisdn,
        amount_minor: req.amount_minor,
        cycle_id: req.cycle_id,
        provider: req.provider,
        timestamp: req.timestamp.unwrap_or_else(OffsetDateTime::now_utc),
    };

    match state.contribution_service.handle_callback(callback).await {
        Ok(CallbackOutcome::Reconciled) => (
            StatusCode::OK,
            Json(StatusResponse {
                status: "reconciled",
                service: "masecure-kernel",
            }),
        ),
        Ok(CallbackOutcome::Quarantined) => (
            StatusCode::OK,
            Json(StatusResponse {
                status: "quarantined",
                service: "masecure-kernel",
            }),
        ),
        Ok(CallbackOutcome::AlreadyProcessed) => (
            StatusCode::OK,
            Json(StatusResponse {
                status: "already_processed",
                service: "masecure-kernel",
            }),
        ),
        Ok(CallbackOutcome::IgnoredCycleState) => (
            StatusCode::OK,
            Json(StatusResponse {
                status: "ignored_cycle_state",
                service: "masecure-kernel",
            }),
        ),
        Err(err) => {
            tracing::error!("Contribution callback failed: {}", err);
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(StatusResponse {
                    status: "error",
                    service: "masecure-kernel",
                }),
            )
        }
    }
}

async fn handle_payout_confirmation(
    State(state): State<KernelHttpState>,
    Json(req): Json<InternalPayoutConfirmationRequest>,
) -> impl IntoResponse {
    match state
        .payout_service
        .confirm_payout(req.cycle_id, req.external_ref)
        .await
    {
        Ok(PayoutConfirmationOutcome::Confirmed) => (
            StatusCode::OK,
            Json(StatusResponse {
                status: "confirmed",
                service: "masecure-kernel",
            }),
        ),
        Ok(PayoutConfirmationOutcome::AlreadyConfirmed) => (
            StatusCode::OK,
            Json(StatusResponse {
                status: "already_confirmed",
                service: "masecure-kernel",
            }),
        ),
        Err(err) => {
            tracing::error!("Payout confirmation failed: {}", err);
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(StatusResponse {
                    status: "error",
                    service: "masecure-kernel",
                }),
            )
        }
    }
}

async fn handle_payout_failure(
    State(state): State<KernelHttpState>,
    Json(req): Json<InternalPayoutFailureRequest>,
) -> impl IntoResponse {
    match state
        .payout_service
        .fail_payout(req.cycle_id, req.external_ref, req.reason)
        .await
    {
        Ok(PayoutFailureOutcome::Failed) => (
            StatusCode::OK,
            Json(StatusResponse {
                status: "failed",
                service: "masecure-kernel",
            }),
        ),
        Ok(PayoutFailureOutcome::AlreadyFailed) => (
            StatusCode::OK,
            Json(StatusResponse {
                status: "already_failed",
                service: "masecure-kernel",
            }),
        ),
        Ok(PayoutFailureOutcome::IgnoredAlreadyConfirmed) => (
            StatusCode::OK,
            Json(StatusResponse {
                status: "ignored_already_confirmed",
                service: "masecure-kernel",
            }),
        ),
        Err(err) => {
            tracing::error!("Payout failure handling failed: {}", err);
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(StatusResponse {
                    status: "error",
                    service: "masecure-kernel",
                }),
            )
        }
    }
}
