//! Outil CLI de vérification d'intégrité du ledger MaSecure
//!
//! Usage :
//!   DATABASE_URL=postgresql://... cargo run --bin verify-ledger
//!   DATABASE_URL=postgresql://... cargo run --bin verify-ledger -- --aggregate-type cycle --aggregate-id <UUID>
//!
//! Retourne un rapport JSON sur stdout.
//! Code de sortie : 0 si intégrité valide, 1 si violations détectées.

use anyhow::Context;
use std::process;
use tracing::info;

use masecure_kernel::domain::ledger::verify_chain;
use masecure_kernel::infrastructure::pg_ledger_repo::PgLedgerRepository;
use masecure_kernel::ports::ledger_repo::LedgerRepository;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt().with_env_filter("warn").init();

    // Parsing des arguments
    let args: Vec<String> = std::env::args().collect();
    let (agg_type, agg_id) = parse_args(&args);

    // Connexion PostgreSQL
    let db_url = std::env::var("DATABASE_URL").context("DATABASE_URL required")?;
    let pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(2)
        .connect(&db_url)
        .await
        .context("Failed to connect to PostgreSQL")?;

    let repo = PgLedgerRepository::new(pool);

    // Lecture du ledger
    let entries = if let (Some(at), Some(id)) = (&agg_type, &agg_id) {
        let uuid = uuid::Uuid::parse_str(id).context("Invalid UUID for aggregate-id")?;
        info!("Verifying ledger for {} {}", at, uuid);
        repo.get_by_aggregate(at, uuid).await?
    } else {
        info!("Verifying entire ledger");
        repo.get_all_ordered().await?
    };

    info!("Read {} ledger entries", entries.len());

    // Vérification de la chaîne
    let report = verify_chain(&entries);

    // Sortie JSON
    let json = serde_json::to_string_pretty(&report).context("Failed to serialize report")?;
    println!("{}", json);

    if report.is_valid {
        eprintln!(
            "✓ Ledger integrity: VALID ({} entries)",
            report.total_entries
        );
        process::exit(0);
    } else {
        eprintln!(
            "✗ Ledger integrity: VIOLATIONS DETECTED ({} of {} entries)",
            report.violations.len(),
            report.total_entries
        );
        process::exit(1);
    }
}

fn parse_args(args: &[String]) -> (Option<String>, Option<String>) {
    let mut agg_type = None;
    let mut agg_id = None;
    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--aggregate-type" if i + 1 < args.len() => {
                agg_type = Some(args[i + 1].clone());
                i += 2;
            }
            "--aggregate-id" if i + 1 < args.len() => {
                agg_id = Some(args[i + 1].clone());
                i += 2;
            }
            _ => {
                i += 1;
            }
        }
    }
    (agg_type, agg_id)
}
