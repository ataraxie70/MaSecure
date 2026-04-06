//! Couche Infrastructure — implémentations concrètes des ports
//!
//! Cette couche contient tout le code qui "touche" la base de données.
//! Elle implémente les traits définis dans crate::ports.
//! Le domaine n'en dépend JAMAIS : la dépendance va toujours vers l'intérieur.
pub mod pg_contribution_repo;
pub mod pg_cycle_repo;
pub mod pg_identity_repo;
pub mod pg_ledger_repo;
pub mod pg_outbox_repo;
pub mod scheduler;
pub mod pg_working_capital_repo;
pub mod pg_debt_repo;
pub mod resilience_scheduler;
