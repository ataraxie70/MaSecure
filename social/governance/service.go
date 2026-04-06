package governance

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrForbidden        = errors.New("forbidden")
	ErrProposalClosed   = errors.New("proposal is not open")
	ErrDuplicateVote    = errors.New("vote already recorded")
	ErrNoConfigChanges  = errors.New("proposal must include at least one config change")
	ErrInvalidInput     = errors.New("invalid input")
	ErrProposalPending  = errors.New("proposal still pending")
)

type Service struct {
	store ProposalStore
	now   func() time.Time
}

func NewService(store ProposalStore) *Service {
	return &Service{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) GetProposal(ctx context.Context, proposalID string) (ProposalDetails, error) {
	proposal, err := s.store.GetProposal(ctx, proposalID)
	if err != nil {
		return ProposalDetails{}, err
	}

	baseConfig, err := s.store.GetConfig(ctx, proposal.BaseConfigID)
	if err != nil {
		return ProposalDetails{}, err
	}
	newConfig, err := s.store.GetConfig(ctx, proposal.NewConfigID)
	if err != nil {
		return ProposalDetails{}, err
	}
	activeMembers, err := s.store.CountActiveMembers(ctx, proposal.GroupID)
	if err != nil {
		return ProposalDetails{}, err
	}

	return ProposalDetails{
		Proposal:   proposal,
		BaseConfig: baseConfig,
		NewConfig:  newConfig,
		Stats:      computeResolutionStats(proposal.Votes, activeMembers, baseConfig.QuorumPct),
	}, nil
}

func (s *Service) CreateProposal(ctx context.Context, input CreateProposalInput) (ProposalDetails, error) {
	if strings.TrimSpace(input.GroupID) == "" || strings.TrimSpace(input.ProposedBy) == "" {
		return ProposalDetails{}, fmt.Errorf("%w: group_id and proposed_by are required", ErrInvalidInput)
	}

	group, err := s.store.GetGroup(ctx, input.GroupID)
	if err != nil {
		return ProposalDetails{}, err
	}
	if group.ActiveConfigID == nil {
		return ProposalDetails{}, fmt.Errorf("%w: group %s has no active config", ErrInvalidInput, input.GroupID)
	}

	allowed, err := s.isGovernor(ctx, group, input.ProposedBy)
	if err != nil {
		return ProposalDetails{}, err
	}
	if !allowed {
		return ProposalDetails{}, ErrForbidden
	}

	baseConfig, err := s.store.GetConfig(ctx, *group.ActiveConfigID)
	if err != nil {
		return ProposalDetails{}, err
	}
	if baseConfig.State != "committed" {
		return ProposalDetails{}, fmt.Errorf("%w: active config is not committed", ErrInvalidInput)
	}

	nextVersion, err := s.store.GetNextConfigVersion(ctx, input.GroupID)
	if err != nil {
		return ProposalDetails{}, err
	}

	newConfig, err := buildProposedConfig(baseConfig, input.ProposedBy, nextVersion, input.Changes)
	if err != nil {
		return ProposalDetails{}, err
	}

	diffSummary := buildDiffSummary(baseConfig, newConfig)
	if len(diffSummary) == 0 {
		return ProposalDetails{}, ErrNoConfigChanges
	}

	proposal := ProposalRecord{
		ID:           uuid.NewString(),
		GroupID:      input.GroupID,
		ProposedBy:   input.ProposedBy,
		BaseConfigID: baseConfig.ID,
		NewConfigID:  newConfig.ID,
		DiffSummary:  diffSummary,
		Status:       ProposalStatusOpen,
		Votes:        []ProposalVote{},
	}

	createdProposal, err := s.store.CreateProposal(ctx, CreateProposalParams{
		Proposal: proposal,
		NewConfig: newConfig,
	})
	if err != nil {
		return ProposalDetails{}, err
	}

	activeMembers, err := s.store.CountActiveMembers(ctx, input.GroupID)
	if err != nil {
		return ProposalDetails{}, err
	}

	return ProposalDetails{
		Proposal:   createdProposal,
		BaseConfig: baseConfig,
		NewConfig:  newConfig,
		Stats:      computeResolutionStats(createdProposal.Votes, activeMembers, baseConfig.QuorumPct),
	}, nil
}

func (s *Service) CastVote(
	ctx context.Context,
	proposalID string,
	input CastVoteInput,
) (ProposalDetails, error) {
	if strings.TrimSpace(proposalID) == "" || strings.TrimSpace(input.VoterID) == "" {
		return ProposalDetails{}, fmt.Errorf("%w: proposal_id and voter_id are required", ErrInvalidInput)
	}
	if input.Decision != VoteDecisionApprove && input.Decision != VoteDecisionReject {
		return ProposalDetails{}, fmt.Errorf("%w: invalid vote decision", ErrInvalidInput)
	}

	proposal, err := s.store.GetProposal(ctx, proposalID)
	if err != nil {
		return ProposalDetails{}, err
	}
	if proposal.Status != ProposalStatusOpen {
		return ProposalDetails{}, ErrProposalClosed
	}

	group, err := s.store.GetGroup(ctx, proposal.GroupID)
	if err != nil {
		return ProposalDetails{}, err
	}
	allowed, err := s.isGovernor(ctx, group, input.VoterID)
	if err != nil {
		return ProposalDetails{}, err
	}
	if !allowed {
		return ProposalDetails{}, ErrForbidden
	}

	for _, vote := range proposal.Votes {
		if vote.IdentityID == input.VoterID {
			return ProposalDetails{}, ErrDuplicateVote
		}
	}

	proposal.Votes = append(proposal.Votes, ProposalVote{
		IdentityID: input.VoterID,
		Decision:   input.Decision,
		VotedAt:    s.now(),
	})
	if err := s.store.UpdateProposalVotes(ctx, proposal.ID, proposal.Votes); err != nil {
		return ProposalDetails{}, err
	}

	return s.GetProposal(ctx, proposal.ID)
}

func (s *Service) ResolveProposal(
	ctx context.Context,
	proposalID string,
	input ResolveProposalInput,
) (ResolutionResult, error) {
	if strings.TrimSpace(proposalID) == "" || strings.TrimSpace(input.RequestedBy) == "" {
		return ResolutionResult{}, fmt.Errorf("%w: proposal_id and requested_by are required", ErrInvalidInput)
	}

	proposal, err := s.store.GetProposal(ctx, proposalID)
	if err != nil {
		return ResolutionResult{}, err
	}

	group, err := s.store.GetGroup(ctx, proposal.GroupID)
	if err != nil {
		return ResolutionResult{}, err
	}
	allowed, err := s.isGovernor(ctx, group, input.RequestedBy)
	if err != nil {
		return ResolutionResult{}, err
	}
	if !allowed {
		return ResolutionResult{}, ErrForbidden
	}

	baseConfig, err := s.store.GetConfig(ctx, proposal.BaseConfigID)
	if err != nil {
		return ResolutionResult{}, err
	}
	activeMembers, err := s.store.CountActiveMembers(ctx, proposal.GroupID)
	if err != nil {
		return ResolutionResult{}, err
	}

	stats := computeResolutionStats(proposal.Votes, activeMembers, baseConfig.QuorumPct)
	if proposal.Status != ProposalStatusOpen {
		return ResolutionResult{
			Proposal: proposal,
			Stats:    stats,
		}, nil
	}

	now := s.now()
	switch {
	case stats.ApproveVotes >= stats.RequiredYes:
		if err := s.store.MarkProposalApproved(
			ctx,
			proposal.ID,
			proposal.GroupID,
			proposal.BaseConfigID,
			proposal.NewConfigID,
			now,
		); err != nil {
			return ResolutionResult{}, err
		}
		proposal.Status = ProposalStatusApproved
		proposal.ResolvedAt = &now
	case stats.ApproveVotes+stats.PendingVotes < stats.RequiredYes:
		if err := s.store.MarkProposalRejected(ctx, proposal.ID, proposal.NewConfigID, now); err != nil {
			return ResolutionResult{}, err
		}
		proposal.Status = ProposalStatusRejected
		proposal.ResolvedAt = &now
	default:
		return ResolutionResult{
			Proposal: proposal,
			Stats:    stats,
		}, ErrProposalPending
	}

	return ResolutionResult{
		Proposal: proposal,
		Stats:    stats,
	}, nil
}

func (s *Service) isGovernor(ctx context.Context, group GroupRecord, identityID string) (bool, error) {
	if group.FounderID == identityID {
		return true, nil
	}
	return s.store.IsActiveMember(ctx, group.ID, identityID)
}

func buildProposedConfig(
	base ConfigSnapshot,
	proposedBy string,
	nextVersion int,
	patch ConfigPatch,
) (ConfigSnapshot, error) {
	if patch.AmountMinor == nil &&
		patch.Periodicity == nil &&
		patch.PayoutPolicy == nil &&
		patch.MemberOrder == nil &&
		patch.QuorumPct == nil {
		return ConfigSnapshot{}, ErrNoConfigChanges
	}

	proposed := cloneConfig(base)
	proposed.ID = uuid.NewString()
	proposed.VersionNo = nextVersion
	proposed.State = "review"
	proposed.CreatedBy = proposedBy
	proposed.CommittedAt = nil
	proposed.PrevConfigID = &base.ID

	if patch.AmountMinor != nil {
		if *patch.AmountMinor <= 0 {
			return ConfigSnapshot{}, fmt.Errorf("%w: amount_minor must be > 0", ErrInvalidInput)
		}
		proposed.AmountMinor = *patch.AmountMinor
	}
	if patch.Periodicity != nil {
		periodicity := strings.ToLower(strings.TrimSpace(*patch.Periodicity))
		if !slices.Contains([]string{"weekly", "biweekly", "monthly"}, periodicity) {
			return ConfigSnapshot{}, fmt.Errorf("%w: periodicity must be weekly, biweekly or monthly", ErrInvalidInput)
		}
		proposed.Periodicity = periodicity
	}
	if patch.PayoutPolicy != nil {
		proposed.PayoutPolicy = cloneJSONMap(patch.PayoutPolicy)
	}
	if patch.MemberOrder != nil {
		if len(patch.MemberOrder) == 0 {
			return ConfigSnapshot{}, fmt.Errorf("%w: member_order cannot be empty", ErrInvalidInput)
		}
		if err := validateMemberOrder(patch.MemberOrder); err != nil {
			return ConfigSnapshot{}, err
		}
		proposed.MemberOrder = cloneStringSlice(patch.MemberOrder)
	}
	if patch.QuorumPct != nil {
		if *patch.QuorumPct < 51 || *patch.QuorumPct > 100 {
			return ConfigSnapshot{}, fmt.Errorf("%w: quorum_pct must be between 51 and 100", ErrInvalidInput)
		}
		proposed.QuorumPct = *patch.QuorumPct
	}

	if len(buildDiffSummary(base, proposed)) == 0 {
		return ConfigSnapshot{}, ErrNoConfigChanges
	}

	return proposed, nil
}

func validateMemberOrder(values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" {
			return fmt.Errorf("%w: member_order contains blank identity", ErrInvalidInput)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%w: member_order contains duplicate identity %s", ErrInvalidInput, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func buildDiffSummary(base ConfigSnapshot, proposed ConfigSnapshot) ProposalDiff {
	diff := ProposalDiff{}

	if base.AmountMinor != proposed.AmountMinor {
		diff["amount_minor"] = ProposalChange{From: base.AmountMinor, To: proposed.AmountMinor}
	}
	if base.Periodicity != proposed.Periodicity {
		diff["periodicity"] = ProposalChange{From: base.Periodicity, To: proposed.Periodicity}
	}
	if !reflect.DeepEqual(base.PayoutPolicy, proposed.PayoutPolicy) {
		diff["payout_policy"] = ProposalChange{
			From: base.PayoutPolicy,
			To:   proposed.PayoutPolicy,
		}
	}
	if !slices.Equal(base.MemberOrder, proposed.MemberOrder) {
		diff["member_order"] = ProposalChange{
			From: base.MemberOrder,
			To:   proposed.MemberOrder,
		}
	}
	if base.QuorumPct != proposed.QuorumPct {
		diff["quorum_pct"] = ProposalChange{From: base.QuorumPct, To: proposed.QuorumPct}
	}

	return diff
}

func computeResolutionStats(votes []ProposalVote, activeMembers, quorumPct int) ResolutionStats {
	stats := ResolutionStats{
		ActiveMembers: activeMembers,
		RequiredYes:   requiredVotes(activeMembers, quorumPct),
	}

	for _, vote := range votes {
		switch vote.Decision {
		case VoteDecisionApprove:
			stats.ApproveVotes++
		case VoteDecisionReject:
			stats.RejectVotes++
		}
	}
	voted := stats.ApproveVotes + stats.RejectVotes
	if activeMembers > voted {
		stats.PendingVotes = activeMembers - voted
	}

	return stats
}

func requiredVotes(activeMembers, quorumPct int) int {
	if activeMembers <= 0 {
		return 0
	}
	if quorumPct <= 0 {
		return 0
	}
	return (activeMembers*quorumPct + 99) / 100
}
