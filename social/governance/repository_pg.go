package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgProposalStore implémente ProposalStore sur PostgreSQL.
// C'est la seule implémentation concrète utilisée en production.
// Les tests utilisent une implémentation in-memory distincte.
type PgProposalStore struct {
	db *pgxpool.Pool
}

// NewPgProposalStore crée un store PostgreSQL pour la gouvernance.
func NewPgProposalStore(db *pgxpool.Pool) *PgProposalStore {
	return &PgProposalStore{db: db}
}

// ── GetGroup ──────────────────────────────────────────────────────────────────

func (s *PgProposalStore) GetGroup(ctx context.Context, groupID string) (GroupRecord, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, founder_id, active_config_id
		FROM tontine_groups
		WHERE id = $1
	`, groupID)

	var g GroupRecord
	var activeConfigID *string
	err := row.Scan(&g.ID, &g.FounderID, &activeConfigID)
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupRecord{}, ErrNotFound
	}
	if err != nil {
		return GroupRecord{}, fmt.Errorf("get group: %w", err)
	}
	g.ActiveConfigID = activeConfigID
	return g, nil
}

// ── IsActiveMember ────────────────────────────────────────────────────────────

func (s *PgProposalStore) IsActiveMember(ctx context.Context, groupID, identityID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM group_configs gc
			JOIN tontine_groups tg ON tg.active_config_id = gc.id
			WHERE tg.id      = $1
			  AND tg.status  = 'active'
			  AND gc.state   = 'committed'
			  AND $2 = ANY(gc.member_order)
		)
	`, groupID, identityID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("is active member: %w", err)
	}
	return exists, nil
}

// ── CountActiveMembers ────────────────────────────────────────────────────────

func (s *PgProposalStore) CountActiveMembers(ctx context.Context, groupID string) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(array_length(gc.member_order, 1), 0)
		FROM group_configs gc
		JOIN tontine_groups tg ON tg.active_config_id = gc.id
		WHERE tg.id    = $1
		  AND gc.state = 'committed'
	`, groupID).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("count active members: %w", err)
	}
	return count, nil
}

// ── GetConfig ─────────────────────────────────────────────────────────────────

func (s *PgProposalStore) GetConfig(ctx context.Context, configID string) (ConfigSnapshot, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, group_id, version_no, amount_minor, periodicity,
		       payout_policy, member_order, quorum_pct, state,
		       committed_at, created_by, prev_config_id
		FROM group_configs
		WHERE id = $1
	`, configID)

	var c ConfigSnapshot
	var payoutPolicyJSON []byte
	var memberOrder []string
	var committedAt *time.Time
	var prevConfigID *string

	err := row.Scan(
		&c.ID, &c.GroupID, &c.VersionNo, &c.AmountMinor, &c.Periodicity,
		&payoutPolicyJSON, &memberOrder, &c.QuorumPct, &c.State,
		&committedAt, &c.CreatedBy, &prevConfigID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConfigSnapshot{}, ErrNotFound
	}
	if err != nil {
		return ConfigSnapshot{}, fmt.Errorf("get config: %w", err)
	}

	if err := json.Unmarshal(payoutPolicyJSON, &c.PayoutPolicy); err != nil {
		return ConfigSnapshot{}, fmt.Errorf("unmarshal payout_policy: %w", err)
	}
	c.MemberOrder = memberOrder
	c.CommittedAt = committedAt
	c.PrevConfigID = prevConfigID
	return c, nil
}

// ── GetNextConfigVersion ──────────────────────────────────────────────────────

func (s *PgProposalStore) GetNextConfigVersion(ctx context.Context, groupID string) (int, error) {
	var maxVersion int
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(MAX(version_no), 0) + 1
		FROM group_configs
		WHERE group_id = $1
	`, groupID).Scan(&maxVersion)
	if err != nil {
		return 0, fmt.Errorf("get next version: %w", err)
	}
	return maxVersion, nil
}

// ── CreateProposal ────────────────────────────────────────────────────────────

func (s *PgProposalStore) CreateProposal(ctx context.Context, params CreateProposalParams) (ProposalRecord, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Insérer la nouvelle config en état draft
	nc := params.NewConfig
	payoutPolicyJSON, err := json.Marshal(nc.PayoutPolicy)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("marshal payout_policy: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO group_configs (
			id, group_id, version_no, amount_minor, periodicity,
			payout_policy, member_order, quorum_pct, state, created_by, prev_config_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'draft', $9, $10)
	`,
		nc.ID, nc.GroupID, nc.VersionNo, nc.AmountMinor, nc.Periodicity,
		payoutPolicyJSON, nc.MemberOrder, nc.QuorumPct, nc.CreatedBy, nc.PrevConfigID,
	)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("insert new config: %w", err)
	}

	// 2. Insérer la proposition
	p := params.Proposal
	diffJSON, err := json.Marshal(p.DiffSummary)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("marshal diff: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO proposals (
			id, group_id, proposed_by, base_config_id, new_config_id,
			diff_summary, status, quorum_pct, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'open', $7, $8)
	`,
		p.ID, p.GroupID, p.ProposedBy, p.BaseConfigID, p.NewConfigID,
		diffJSON, nc.QuorumPct, p.CreatedAt,
	)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("insert proposal: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ProposalRecord{}, fmt.Errorf("commit create proposal: %w", err)
	}

	return p, nil
}

// ── GetProposal ───────────────────────────────────────────────────────────────

func (s *PgProposalStore) GetProposal(ctx context.Context, proposalID string) (ProposalRecord, error) {
	row := s.db.QueryRow(ctx, `
		SELECT p.id, p.group_id, p.proposed_by, p.base_config_id, p.new_config_id,
		       p.diff_summary, p.status, p.quorum_pct, p.created_at, p.resolved_at,
		       COALESCE(
		           json_agg(json_build_object(
		               'identity_id', pv.identity_id,
		               'decision',    pv.decision,
		               'voted_at',    pv.voted_at
		           )) FILTER (WHERE pv.id IS NOT NULL),
		           '[]'
		       ) AS votes
		FROM proposals p
		LEFT JOIN proposal_votes pv ON pv.proposal_id = p.id
		WHERE p.id = $1
		GROUP BY p.id
	`, proposalID)

	var rec ProposalRecord
	var diffJSON []byte
	var votesJSON []byte
	var resolvedAt *time.Time

	err := row.Scan(
		&rec.ID, &rec.GroupID, &rec.ProposedBy, &rec.BaseConfigID, &rec.NewConfigID,
		&diffJSON, &rec.Status, &rec.Votes, &rec.CreatedAt, &resolvedAt,
		&votesJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProposalRecord{}, ErrNotFound
	}
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("get proposal: %w", err)
	}

	if err := json.Unmarshal(diffJSON, &rec.DiffSummary); err != nil {
		return ProposalRecord{}, fmt.Errorf("unmarshal diff: %w", err)
	}

	var rawVotes []struct {
		IdentityID string    `json:"identity_id"`
		Decision   string    `json:"decision"`
		VotedAt    time.Time `json:"voted_at"`
	}
	if err := json.Unmarshal(votesJSON, &rawVotes); err != nil {
		return ProposalRecord{}, fmt.Errorf("unmarshal votes: %w", err)
	}
	rec.Votes = make([]ProposalVote, len(rawVotes))
	for i, v := range rawVotes {
		rec.Votes[i] = ProposalVote{
			IdentityID: v.IdentityID,
			Decision:   VoteDecision(v.Decision),
			VotedAt:    v.VotedAt,
		}
	}
	rec.ResolvedAt = resolvedAt
	return rec, nil
}

// ── UpdateProposalVotes ────────────────────────────────────────────────────────

func (s *PgProposalStore) UpdateProposalVotes(ctx context.Context, proposalID string, votes []ProposalVote) error {
	// Upsert le dernier vote ajouté (le slice complet est passé par le service)
	if len(votes) == 0 {
		return nil
	}
	last := votes[len(votes)-1]

	_, err := s.db.Exec(ctx, `
		INSERT INTO proposal_votes (id, proposal_id, identity_id, decision, voted_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (proposal_id, identity_id) DO NOTHING
	`,
		uuid.New().String(), proposalID, last.IdentityID, string(last.Decision), last.VotedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert vote: %w", err)
	}
	return nil
}

// ── MarkProposalApproved ─────────────────────────────────────────────────────

func (s *PgProposalStore) MarkProposalApproved(
	ctx context.Context,
	proposalID, groupID, baseConfigID, newConfigID string,
	resolvedAt time.Time,
) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Marquer la proposition comme approuvée
	_, err = tx.Exec(ctx, `
		UPDATE proposals
		SET status = 'approved', resolved_at = $2
		WHERE id = $1
	`, proposalID, resolvedAt)
	if err != nil {
		return fmt.Errorf("mark proposal approved: %w", err)
	}

	// 2. Passer la nouvelle config en state 'committed'
	_, err = tx.Exec(ctx, `
		UPDATE group_configs
		SET state = 'committed', committed_at = $2
		WHERE id = $1
	`, newConfigID, resolvedAt)
	if err != nil {
		return fmt.Errorf("commit new config: %w", err)
	}

	// 3. Passer l'ancienne config en state 'superseded'
	_, err = tx.Exec(ctx, `
		UPDATE group_configs
		SET state = 'superseded'
		WHERE id = $1
	`, baseConfigID)
	if err != nil {
		return fmt.Errorf("supersede old config: %w", err)
	}

	// 4. Mettre à jour active_config_id du groupe
	// NOTE : le changement s'applique au PROCHAIN cycle — le cycle actif
	// garde sa config figée. L'application du changement au cycle suivant
	// est gérée par le scheduler du Kernel lors de l'ouverture du nouveau cycle.
	_, err = tx.Exec(ctx, `
		UPDATE tontine_groups
		SET active_config_id = $2
		WHERE id = $1
	`, groupID, newConfigID)
	if err != nil {
		return fmt.Errorf("update active config: %w", err)
	}

	return tx.Commit(ctx)
}

// ── MarkProposalRejected ──────────────────────────────────────────────────────

func (s *PgProposalStore) MarkProposalRejected(
	ctx context.Context,
	proposalID, newConfigID string,
	resolvedAt time.Time,
) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE proposals
		SET status = 'rejected', resolved_at = $2
		WHERE id = $1
	`, proposalID, resolvedAt)
	if err != nil {
		return fmt.Errorf("mark rejected: %w", err)
	}

	// La nouvelle config draft est abandonnée (on ne la supprime pas pour l'audit)
	_, err = tx.Exec(ctx, `
		UPDATE group_configs
		SET state = 'superseded'
		WHERE id = $1
	`, newConfigID)
	if err != nil {
		return fmt.Errorf("supersede draft config: %w", err)
	}

	return tx.Commit(ctx)
}

// ── ExpireStaleProposals ──────────────────────────────────────────────────────

// ExpireStaleProposals marque comme 'expired' toutes les propositions dont
// le délai de vote est dépassé. Appelée périodiquement par le Social Service.
func (s *PgProposalStore) ExpireStaleProposals(ctx context.Context) (int64, error) {
	res, err := s.db.Exec(ctx, `
		UPDATE proposals
		SET status = 'expired', resolved_at = NOW()
		WHERE status = 'open'
		  AND expires_at < NOW()
	`)
	if err != nil {
		return 0, fmt.Errorf("expire proposals: %w", err)
	}
	return res.RowsAffected(), nil
}

// ── ListGroupProposals ────────────────────────────────────────────────────────

// ListGroupProposals retourne les propositions d'un groupe avec leurs statistiques.
// Utile pour le dashboard de gouvernance et les notifications de vote.
func (s *PgProposalStore) ListGroupProposals(ctx context.Context, groupID string, statusFilter string) ([]ProposalRecord, error) {
	query := `
		SELECT p.id, p.group_id, p.proposed_by, p.base_config_id, p.new_config_id,
		       p.diff_summary, p.status, p.quorum_pct, p.created_at, p.resolved_at
		FROM proposals p
		WHERE p.group_id = $1
	`
	args := []any{groupID}
	if statusFilter != "" {
		query += " AND p.status = $2"
		args = append(args, statusFilter)
	}
	query += " ORDER BY p.created_at DESC LIMIT 50"

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list proposals: %w", err)
	}
	defer rows.Close()

	var results []ProposalRecord
	for rows.Next() {
		var rec ProposalRecord
		var diffJSON []byte
		var resolvedAt *time.Time

		if err := rows.Scan(
			&rec.ID, &rec.GroupID, &rec.ProposedBy, &rec.BaseConfigID, &rec.NewConfigID,
			&diffJSON, &rec.Status, &rec.Votes, &rec.CreatedAt, &resolvedAt,
		); err != nil {
			return nil, fmt.Errorf("scan proposal: %w", err)
		}
		if err := json.Unmarshal(diffJSON, &rec.DiffSummary); err != nil {
			return nil, fmt.Errorf("unmarshal diff: %w", err)
		}
		rec.ResolvedAt = resolvedAt
		results = append(results, rec)
	}
	return results, rows.Err()
}
