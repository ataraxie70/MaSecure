package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) GetGroup(ctx context.Context, groupID string) (GroupRecord, error) {
	var group GroupRecord
	err := r.pool.QueryRow(
		ctx,
		`SELECT id::text, founder_id::text, active_config_id::text
		 FROM tontine_groups
		 WHERE id = $1`,
		groupID,
	).Scan(&group.ID, &group.FounderID, &group.ActiveConfigID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GroupRecord{}, ErrNotFound
		}
		return GroupRecord{}, fmt.Errorf("get group: %w", err)
	}
	return group, nil
}

func (r *Repository) IsActiveMember(ctx context.Context, groupID, identityID string) (bool, error) {
	var allowed bool
	err := r.pool.QueryRow(
		ctx,
		`SELECT EXISTS(
			SELECT 1
			FROM group_members
			WHERE group_id = $1
			  AND identity_id = $2
			  AND status = 'active'
		)`,
		groupID,
		identityID,
	).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check active member: %w", err)
	}
	return allowed, nil
}

func (r *Repository) CountActiveMembers(ctx context.Context, groupID string) (int, error) {
	var count int
	err := r.pool.QueryRow(
		ctx,
		`SELECT COUNT(*)
		 FROM group_members
		 WHERE group_id = $1
		   AND status = 'active'`,
		groupID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count active members: %w", err)
	}
	return count, nil
}

func (r *Repository) GetConfig(ctx context.Context, configID string) (ConfigSnapshot, error) {
	return getConfig(ctx, r.pool, configID)
}

func getConfig(ctx context.Context, q queryer, configID string) (ConfigSnapshot, error) {
	var (
		config       ConfigSnapshot
		policyRaw    []byte
		memberOrder  []string
		committedAt  *time.Time
		prevConfigID *string
	)

	err := q.QueryRow(
		ctx,
		`SELECT
			id::text,
			group_id::text,
			version_no,
			amount_minor,
			periodicity::text,
			payout_policy,
			ARRAY(SELECT member::text FROM unnest(member_order) AS member) AS member_order_text,
			quorum_pct,
			state::text,
			committed_at,
			created_by::text,
			prev_config_id::text
		FROM group_configs
		WHERE id = $1`,
		configID,
	).Scan(
		&config.ID,
		&config.GroupID,
		&config.VersionNo,
		&config.AmountMinor,
		&config.Periodicity,
		&policyRaw,
		&memberOrder,
		&config.QuorumPct,
		&config.State,
		&committedAt,
		&config.CreatedBy,
		&prevConfigID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfigSnapshot{}, ErrNotFound
		}
		return ConfigSnapshot{}, fmt.Errorf("get config: %w", err)
	}

	config.MemberOrder = memberOrder
	config.CommittedAt = committedAt
	config.PrevConfigID = prevConfigID
	if len(policyRaw) > 0 {
		if err := json.Unmarshal(policyRaw, &config.PayoutPolicy); err != nil {
			return ConfigSnapshot{}, fmt.Errorf("decode payout_policy: %w", err)
		}
	}

	return config, nil
}

func (r *Repository) GetNextConfigVersion(ctx context.Context, groupID string) (int, error) {
	var version int
	err := r.pool.QueryRow(
		ctx,
		`SELECT COALESCE(MAX(version_no), 0) + 1
		 FROM group_configs
		 WHERE group_id = $1`,
		groupID,
	).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("get next config version: %w", err)
	}
	return version, nil
}

func (r *Repository) CreateProposal(
	ctx context.Context,
	params CreateProposalParams,
) (ProposalRecord, error) {
	diffRaw, err := json.Marshal(params.Proposal.DiffSummary)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("marshal diff summary: %w", err)
	}
	votesRaw, err := json.Marshal(params.Proposal.Votes)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("marshal proposal votes: %w", err)
	}
	payoutPolicyRaw, err := json.Marshal(params.NewConfig.PayoutPolicy)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("marshal payout policy: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("begin create proposal tx: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(
		ctx,
		`INSERT INTO group_configs (
			id, group_id, version_no, amount_minor, periodicity, payout_policy,
			member_order, quorum_pct, state, created_by, prev_config_id
		) VALUES (
			$1, $2, $3, $4, $5::cycle_period, $6, $7::uuid[], $8, $9::config_state, $10, $11
		)`,
		params.NewConfig.ID,
		params.NewConfig.GroupID,
		params.NewConfig.VersionNo,
		params.NewConfig.AmountMinor,
		params.NewConfig.Periodicity,
		payoutPolicyRaw,
		params.NewConfig.MemberOrder,
		params.NewConfig.QuorumPct,
		params.NewConfig.State,
		params.NewConfig.CreatedBy,
		params.NewConfig.PrevConfigID,
	)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("insert new config version: %w", err)
	}

	var createdAt time.Time
	err = tx.QueryRow(
		ctx,
		`INSERT INTO config_proposals (
			id, group_id, proposed_by, base_config_id, new_config_id, diff_summary, status, votes
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
		RETURNING created_at`,
		params.Proposal.ID,
		params.Proposal.GroupID,
		params.Proposal.ProposedBy,
		params.Proposal.BaseConfigID,
		params.Proposal.NewConfigID,
		diffRaw,
		params.Proposal.Status,
		votesRaw,
	).Scan(&createdAt)
	if err != nil {
		return ProposalRecord{}, fmt.Errorf("insert config proposal: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ProposalRecord{}, fmt.Errorf("commit create proposal tx: %w", err)
	}

	proposal := cloneProposal(params.Proposal)
	proposal.CreatedAt = createdAt
	return proposal, nil
}

func (r *Repository) GetProposal(ctx context.Context, proposalID string) (ProposalRecord, error) {
	var (
		proposal  ProposalRecord
		diffRaw   []byte
		votesRaw  []byte
		resolvedAt *time.Time
	)

	err := r.pool.QueryRow(
		ctx,
		`SELECT
			id::text,
			group_id::text,
			proposed_by::text,
			base_config_id::text,
			new_config_id::text,
			diff_summary,
			status,
			votes,
			created_at,
			resolved_at
		FROM config_proposals
		WHERE id = $1`,
		proposalID,
	).Scan(
		&proposal.ID,
		&proposal.GroupID,
		&proposal.ProposedBy,
		&proposal.BaseConfigID,
		&proposal.NewConfigID,
		&diffRaw,
		&proposal.Status,
		&votesRaw,
		&proposal.CreatedAt,
		&resolvedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProposalRecord{}, ErrNotFound
		}
		return ProposalRecord{}, fmt.Errorf("get proposal: %w", err)
	}

	if len(diffRaw) > 0 {
		if err := json.Unmarshal(diffRaw, &proposal.DiffSummary); err != nil {
			return ProposalRecord{}, fmt.Errorf("decode diff summary: %w", err)
		}
	}
	if len(votesRaw) > 0 {
		if err := json.Unmarshal(votesRaw, &proposal.Votes); err != nil {
			return ProposalRecord{}, fmt.Errorf("decode proposal votes: %w", err)
		}
	}
	proposal.ResolvedAt = resolvedAt

	return proposal, nil
}

func (r *Repository) UpdateProposalVotes(
	ctx context.Context,
	proposalID string,
	votes []ProposalVote,
) error {
	votesRaw, err := json.Marshal(votes)
	if err != nil {
		return fmt.Errorf("marshal proposal votes: %w", err)
	}

	commandTag, err := r.pool.Exec(
		ctx,
		`UPDATE config_proposals
		 SET votes = $2
		 WHERE id = $1`,
		proposalID,
		votesRaw,
	)
	if err != nil {
		return fmt.Errorf("update proposal votes: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) MarkProposalApproved(
	ctx context.Context,
	proposalID string,
	groupID string,
	baseConfigID string,
	newConfigID string,
	resolvedAt time.Time,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin approve proposal tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(
		ctx,
		`UPDATE config_proposals
		 SET status = 'approved', resolved_at = $2
		 WHERE id = $1`,
		proposalID,
		resolvedAt,
	); err != nil {
		return fmt.Errorf("mark proposal approved: %w", err)
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE group_configs
		 SET state = 'superseded'
		 WHERE id = $1`,
		baseConfigID,
	); err != nil {
		return fmt.Errorf("supersede base config: %w", err)
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE group_configs
		 SET state = 'committed', committed_at = $2
		 WHERE id = $1`,
		newConfigID,
		resolvedAt,
	); err != nil {
		return fmt.Errorf("commit new config: %w", err)
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE tontine_groups
		 SET active_config_id = $2, updated_at = NOW()
		 WHERE id = $1`,
		groupID,
		newConfigID,
	); err != nil {
		return fmt.Errorf("update active config: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit approve proposal tx: %w", err)
	}
	return nil
}

func (r *Repository) MarkProposalRejected(
	ctx context.Context,
	proposalID string,
	newConfigID string,
	resolvedAt time.Time,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reject proposal tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(
		ctx,
		`UPDATE config_proposals
		 SET status = 'rejected', resolved_at = $2
		 WHERE id = $1`,
		proposalID,
		resolvedAt,
	); err != nil {
		return fmt.Errorf("mark proposal rejected: %w", err)
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE group_configs
		 SET state = 'superseded'
		 WHERE id = $1`,
		newConfigID,
	); err != nil {
		return fmt.Errorf("supersede rejected config: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reject proposal tx: %w", err)
	}
	return nil
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
