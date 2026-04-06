package governance

import (
	"context"
	"encoding/json"
	"time"
)

type ProposalStatus string

const (
	ProposalStatusOpen     ProposalStatus = "open"
	ProposalStatusApproved ProposalStatus = "approved"
	ProposalStatusRejected ProposalStatus = "rejected"
	ProposalStatusExpired  ProposalStatus = "expired"
)

type VoteDecision string

const (
	VoteDecisionApprove VoteDecision = "approve"
	VoteDecisionReject  VoteDecision = "reject"
)

type GroupRecord struct {
	ID             string
	FounderID      string
	ActiveConfigID *string
}

type ConfigSnapshot struct {
	ID           string                 `json:"id"`
	GroupID      string                 `json:"group_id"`
	VersionNo    int                    `json:"version_no"`
	AmountMinor  int64                  `json:"amount_minor"`
	Periodicity  string                 `json:"periodicity"`
	PayoutPolicy map[string]any         `json:"payout_policy"`
	MemberOrder  []string               `json:"member_order"`
	QuorumPct    int                    `json:"quorum_pct"`
	State        string                 `json:"state"`
	CommittedAt  *time.Time             `json:"committed_at,omitempty"`
	CreatedBy    string                 `json:"created_by"`
	PrevConfigID *string                `json:"prev_config_id,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

type ProposalVote struct {
	IdentityID string       `json:"identity_id"`
	Decision   VoteDecision `json:"decision"`
	VotedAt    time.Time    `json:"voted_at"`
}

type ProposalChange struct {
	From any `json:"from"`
	To   any `json:"to"`
}

type ProposalDiff map[string]ProposalChange

type ProposalRecord struct {
	ID           string         `json:"id"`
	GroupID      string         `json:"group_id"`
	ProposedBy   string         `json:"proposed_by"`
	BaseConfigID string         `json:"base_config_id"`
	NewConfigID  string         `json:"new_config_id"`
	DiffSummary  ProposalDiff   `json:"diff_summary"`
	Status       ProposalStatus `json:"status"`
	Votes        []ProposalVote `json:"votes"`
	CreatedAt    time.Time      `json:"created_at"`
	ResolvedAt   *time.Time     `json:"resolved_at,omitempty"`
}

type ProposalDetails struct {
	Proposal   ProposalRecord  `json:"proposal"`
	BaseConfig ConfigSnapshot  `json:"base_config"`
	NewConfig  ConfigSnapshot  `json:"new_config"`
	Stats      ResolutionStats `json:"stats"`
}

type ConfigPatch struct {
	AmountMinor  *int64         `json:"amount_minor,omitempty"`
	Periodicity  *string        `json:"periodicity,omitempty"`
	PayoutPolicy map[string]any `json:"payout_policy,omitempty"`
	MemberOrder  []string       `json:"member_order,omitempty"`
	QuorumPct    *int           `json:"quorum_pct,omitempty"`
}

type CreateProposalInput struct {
	GroupID    string     `json:"group_id"`
	ProposedBy string     `json:"proposed_by"`
	Changes    ConfigPatch `json:"changes"`
}

type CastVoteInput struct {
	VoterID  string       `json:"voter_id"`
	Decision VoteDecision `json:"decision"`
}

type ResolveProposalInput struct {
	RequestedBy string `json:"requested_by"`
}

type ResolutionStats struct {
	ActiveMembers int `json:"active_members"`
	RequiredYes   int `json:"required_yes"`
	ApproveVotes  int `json:"approve_votes"`
	RejectVotes   int `json:"reject_votes"`
	PendingVotes  int `json:"pending_votes"`
}

type ResolutionResult struct {
	Proposal ProposalRecord  `json:"proposal"`
	Stats    ResolutionStats `json:"stats"`
}

type CreateProposalParams struct {
	Proposal  ProposalRecord
	NewConfig ConfigSnapshot
}

type ProposalStore interface {
	GetGroup(ctx context.Context, groupID string) (GroupRecord, error)
	IsActiveMember(ctx context.Context, groupID, identityID string) (bool, error)
	CountActiveMembers(ctx context.Context, groupID string) (int, error)
	GetConfig(ctx context.Context, configID string) (ConfigSnapshot, error)
	GetNextConfigVersion(ctx context.Context, groupID string) (int, error)
	CreateProposal(ctx context.Context, params CreateProposalParams) (ProposalRecord, error)
	GetProposal(ctx context.Context, proposalID string) (ProposalRecord, error)
	UpdateProposalVotes(ctx context.Context, proposalID string, votes []ProposalVote) error
	MarkProposalApproved(
		ctx context.Context,
		proposalID string,
		groupID string,
		baseConfigID string,
		newConfigID string,
		resolvedAt time.Time,
	) error
	MarkProposalRejected(
		ctx context.Context,
		proposalID string,
		newConfigID string,
		resolvedAt time.Time,
	) error
}

func cloneStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneJSONMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	raw, _ := json.Marshal(values)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func cloneVotes(values []ProposalVote) []ProposalVote {
	if values == nil {
		return nil
	}
	out := make([]ProposalVote, len(values))
	copy(out, values)
	return out
}

func cloneDiff(values ProposalDiff) ProposalDiff {
	if values == nil {
		return nil
	}
	raw, _ := json.Marshal(values)
	var out ProposalDiff
	_ = json.Unmarshal(raw, &out)
	return out
}

func cloneConfig(config ConfigSnapshot) ConfigSnapshot {
	config.MemberOrder = cloneStringSlice(config.MemberOrder)
	config.PayoutPolicy = cloneJSONMap(config.PayoutPolicy)
	if config.Metadata != nil {
		raw, _ := json.Marshal(config.Metadata)
		var metadata map[string]interface{}
		_ = json.Unmarshal(raw, &metadata)
		config.Metadata = metadata
	}
	return config
}

func cloneProposal(proposal ProposalRecord) ProposalRecord {
	proposal.Votes = cloneVotes(proposal.Votes)
	proposal.DiffSummary = cloneDiff(proposal.DiffSummary)
	return proposal
}
