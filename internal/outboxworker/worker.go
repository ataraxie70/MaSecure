// Package outboxworker implémente le pattern Transactional Outbox Worker.
package outboxworker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const (
	MaxAttempts  = 10
	PollInterval = 5 * time.Second
	BatchSize    = 50
)

type OutboxRow struct {
	ID             uuid.UUID
	EventType      string
	AggregateID    uuid.UUID
	Payload        json.RawMessage
	TargetService  string
	IdempotencyKey string
	Attempts       int16
}

// Dispatcher envoie un événement vers le service cible.
type Dispatcher interface {
	CanHandle(targetService string) bool
	Dispatch(ctx context.Context, row OutboxRow) (externalRef string, err error)
}

type Worker struct {
	db          *pgxpool.Pool
	dispatchers []Dispatcher
	log         *zap.Logger
}

func NewWorker(db *pgxpool.Pool, dispatchers []Dispatcher, log *zap.Logger) *Worker {
	return &Worker{db: db, dispatchers: dispatchers, log: log}
}

// Run démarre la boucle de polling. S'arrête quand ctx est annulé.
func (w *Worker) Run(ctx context.Context) {
	w.log.Info("Outbox Worker started", zap.Duration("poll_interval", PollInterval))
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.log.Info("Outbox Worker stopping")
			return
		case <-ticker.C:
			if err := w.processBatch(ctx); err != nil {
				w.log.Error("Outbox batch error", zap.Error(err))
			}
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) error {
	rows, err := w.claimPending(ctx)
	if err != nil {
		return fmt.Errorf("claimPending: %w", err)
	}
	for _, row := range rows {
		if err := w.processOne(ctx, row); err != nil {
			w.log.Error("outbox event failed",
				zap.String("id", row.ID.String()),
				zap.Error(err),
			)
		}
	}
	return nil
}

func (w *Worker) processOne(ctx context.Context, row OutboxRow) error {
	d := w.findDispatcher(row.TargetService)
	if d == nil {
		return w.scheduleRetry(ctx, row, "no dispatcher for: "+row.TargetService)
	}
	externalRef, err := d.Dispatch(ctx, row)
	if err != nil {
		if int(row.Attempts+1) >= MaxAttempts {
			w.log.Error("dead_letter threshold reached", zap.String("id", row.ID.String()))
			return w.markDeadLetter(ctx, row.ID, err.Error())
		}
		return w.scheduleRetry(ctx, row, err.Error())
	}
	return w.markDelivered(ctx, row.ID, externalRef)
}

func (w *Worker) findDispatcher(target string) Dispatcher {
	for _, d := range w.dispatchers {
		if d.CanHandle(target) {
			return d
		}
	}
	return nil
}

// backoffDuration : délai exponentiel plafonné à 24h
func backoffDuration(attempts int16) time.Duration {
	delay := 30 * time.Second * (1 << uint(attempts))
	if delay > 24*time.Hour {
		return 24 * time.Hour
	}
	return delay
}

func (w *Worker) claimPending(ctx context.Context) ([]OutboxRow, error) {
	rows, err := w.db.Query(ctx, `
        WITH claimed AS (
            SELECT id
            FROM outbox_events
            WHERE status IN ('pending','failed')
              AND next_retry_at <= NOW()
            ORDER BY next_retry_at ASC
            LIMIT $1
            FOR UPDATE SKIP LOCKED
        )
        UPDATE outbox_events o
        SET status = 'processing'
        FROM claimed
        WHERE o.id = claimed.id
        RETURNING o.id, o.event_type, o.aggregate_id, o.payload, o.target_service, o.idempotency_key, o.attempts
    `, BatchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []OutboxRow
	for rows.Next() {
		var r OutboxRow
		var raw []byte
		if err := rows.Scan(&r.ID, &r.EventType, &r.AggregateID, &raw,
			&r.TargetService, &r.IdempotencyKey, &r.Attempts); err != nil {
			return nil, err
		}
		r.Payload = raw
		result = append(result, r)
	}
	return result, rows.Err()
}

func (w *Worker) markDelivered(ctx context.Context, id uuid.UUID, ref string) error {
	_, err := w.db.Exec(ctx,
		`UPDATE outbox_events SET status='delivered', delivered_at=NOW(), error_detail=NULL WHERE id=$1`, id)
	if err == nil {
		w.log.Info("outbox event delivered", zap.String("id", id.String()), zap.String("ref", ref))
	}
	return err
}

func (w *Worker) scheduleRetry(ctx context.Context, row OutboxRow, errMsg string) error {
	na := row.Attempts + 1
	next := time.Now().Add(backoffDuration(na))
	_, err := w.db.Exec(ctx,
		`UPDATE outbox_events SET status='failed', attempts=$1, next_retry_at=$2, error_detail=$3 WHERE id=$4`,
		na, next, errMsg, row.ID)
	w.log.Warn("outbox retry scheduled",
		zap.String("id", row.ID.String()),
		zap.Int16("attempts", na),
		zap.Time("next", next),
	)
	return err
}

func (w *Worker) markDeadLetter(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := w.db.Exec(ctx,
		`UPDATE outbox_events SET status='dead_letter', error_detail=$1 WHERE id=$2`, errMsg, id)
	return err
}
