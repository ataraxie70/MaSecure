package governance

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	groups     map[string]GroupRecord
	members    map[string]map[string]bool
	configs    map[string]ConfigSnapshot
	proposals  map[string]ProposalRecord
	nextConfig map[string]int
}

func newFakeStore() *fakeStore {
	baseConfigID := "config-base"
	return &fakeStore{
		groups: map[string]GroupRecord{
			"group-1": {
				ID:             "group-1",
				FounderID:      "founder-1",
				ActiveConfigID: &baseConfigID,
			},
		},
		members: map[string]map[string]bool{
			"group-1": {
				"member-1": true,
				"member-2": true,
				"member-3": true,
			},
		},
		configs: map[string]ConfigSnapshot{
			baseConfigID: {
				ID:          baseConfigID,
				GroupID:     "group-1",
				VersionNo:   1,
				AmountMinor: 10000,
				Periodicity: "monthly",
				PayoutPolicy: map[string]any{
					"threshold_pct":     float64(100),
					"advance_enabled":   false,
					"pro_rata_enabled":  false,
				},
				MemberOrder: []string{"member-1", "member-2", "member-3"},
				QuorumPct:   67,
				State:       "committed",
				CreatedBy:   "founder-1",
			},
		},
		proposals:  map[string]ProposalRecord{},
		nextConfig: map[string]int{"group-1": 2},
	}
}

func (f *fakeStore) GetGroup(_ context.Context, groupID string) (GroupRecord, error) {
	group, ok := f.groups[groupID]
	if !ok {
		return GroupRecord{}, ErrNotFound
	}
	return group, nil
}

func (f *fakeStore) IsActiveMember(_ context.Context, groupID, identityID string) (bool, error) {
	return f.members[groupID][identityID], nil
}

func (f *fakeStore) CountActiveMembers(_ context.Context, groupID string) (int, error) {
	return len(f.members[groupID]), nil
}

func (f *fakeStore) GetConfig(_ context.Context, configID string) (ConfigSnapshot, error) {
	config, ok := f.configs[configID]
	if !ok {
		return ConfigSnapshot{}, ErrNotFound
	}
	return cloneConfig(config), nil
}

func (f *fakeStore) GetNextConfigVersion(_ context.Context, groupID string) (int, error) {
	return f.nextConfig[groupID], nil
}

func (f *fakeStore) CreateProposal(_ context.Context, params CreateProposalParams) (ProposalRecord, error) {
	proposal := cloneProposal(params.Proposal)
	proposal.CreatedAt = time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	f.proposals[proposal.ID] = proposal
	f.configs[params.NewConfig.ID] = cloneConfig(params.NewConfig)
	f.nextConfig[proposal.GroupID]++
	return proposal, nil
}

func (f *fakeStore) GetProposal(_ context.Context, proposalID string) (ProposalRecord, error) {
	proposal, ok := f.proposals[proposalID]
	if !ok {
		return ProposalRecord{}, ErrNotFound
	}
	return cloneProposal(proposal), nil
}

func (f *fakeStore) UpdateProposalVotes(_ context.Context, proposalID string, votes []ProposalVote) error {
	proposal, ok := f.proposals[proposalID]
	if !ok {
		return ErrNotFound
	}
	proposal.Votes = cloneVotes(votes)
	f.proposals[proposalID] = proposal
	return nil
}

func (f *fakeStore) MarkProposalApproved(
	_ context.Context,
	proposalID string,
	groupID string,
	baseConfigID string,
	newConfigID string,
	resolvedAt time.Time,
) error {
	proposal, ok := f.proposals[proposalID]
	if !ok {
		return ErrNotFound
	}
	proposal.Status = ProposalStatusApproved
	proposal.ResolvedAt = &resolvedAt
	f.proposals[proposalID] = proposal

	group := f.groups[groupID]
	group.ActiveConfigID = &newConfigID
	f.groups[groupID] = group

	base := f.configs[baseConfigID]
	base.State = "superseded"
	f.configs[baseConfigID] = base

	newConfig := f.configs[newConfigID]
	newConfig.State = "committed"
	newConfig.CommittedAt = &resolvedAt
	f.configs[newConfigID] = newConfig
	return nil
}

func (f *fakeStore) MarkProposalRejected(
	_ context.Context,
	proposalID string,
	newConfigID string,
	resolvedAt time.Time,
) error {
	proposal, ok := f.proposals[proposalID]
	if !ok {
		return ErrNotFound
	}
	proposal.Status = ProposalStatusRejected
	proposal.ResolvedAt = &resolvedAt
	f.proposals[proposalID] = proposal

	newConfig := f.configs[newConfigID]
	newConfig.State = "superseded"
	f.configs[newConfigID] = newConfig
	return nil
}

func TestCreateProposalBuildsDiffAndReviewConfig(t *testing.T) {
	store := newFakeStore()
	service := NewService(store)

	newAmount := int64(15000)
	newQuorum := 75
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		GroupID:    "group-1",
		ProposedBy: "founder-1",
		Changes: ConfigPatch{
			AmountMinor: &newAmount,
			QuorumPct:   &newQuorum,
		},
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	if proposal.Proposal.Status != ProposalStatusOpen {
		t.Fatalf("unexpected proposal status %s", proposal.Proposal.Status)
	}
	if proposal.NewConfig.State != "review" {
		t.Fatalf("unexpected new config state %s", proposal.NewConfig.State)
	}
	if proposal.NewConfig.AmountMinor != 15000 {
		t.Fatalf("unexpected amount %d", proposal.NewConfig.AmountMinor)
	}
	if got := proposal.Proposal.DiffSummary["amount_minor"].To; got != float64(15000) && got != int64(15000) {
		t.Fatalf("unexpected diff summary amount %+v", proposal.Proposal.DiffSummary)
	}
}

func TestCastVoteRejectsDuplicateVote(t *testing.T) {
	store := newFakeStore()
	service := NewService(store)

	newAmount := int64(15000)
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		GroupID:    "group-1",
		ProposedBy: "founder-1",
		Changes:    ConfigPatch{AmountMinor: &newAmount},
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	_, err = service.CastVote(context.Background(), proposal.Proposal.ID, CastVoteInput{
		VoterID:  "member-1",
		Decision: VoteDecisionApprove,
	})
	if err != nil {
		t.Fatalf("cast first vote: %v", err)
	}

	_, err = service.CastVote(context.Background(), proposal.Proposal.ID, CastVoteInput{
		VoterID:  "member-1",
		Decision: VoteDecisionApprove,
	})
	if !errors.Is(err, ErrDuplicateVote) {
		t.Fatalf("expected duplicate vote error, got %v", err)
	}
}

func TestResolveProposalApprovesWhenQuorumReached(t *testing.T) {
	store := newFakeStore()
	service := NewService(store)

	newAmount := int64(15000)
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		GroupID:    "group-1",
		ProposedBy: "founder-1",
		Changes:    ConfigPatch{AmountMinor: &newAmount},
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	_, _ = service.CastVote(context.Background(), proposal.Proposal.ID, CastVoteInput{
		VoterID:  "member-1",
		Decision: VoteDecisionApprove,
	})
	_, _ = service.CastVote(context.Background(), proposal.Proposal.ID, CastVoteInput{
		VoterID:  "member-2",
		Decision: VoteDecisionApprove,
	})
	_, _ = service.CastVote(context.Background(), proposal.Proposal.ID, CastVoteInput{
		VoterID:  "member-3",
		Decision: VoteDecisionApprove,
	})

	result, err := service.ResolveProposal(context.Background(), proposal.Proposal.ID, ResolveProposalInput{
		RequestedBy: "founder-1",
	})
	if err != nil {
		t.Fatalf("resolve proposal: %v", err)
	}
	if result.Proposal.Status != ProposalStatusApproved {
		t.Fatalf("unexpected status %s", result.Proposal.Status)
	}
	if store.configs[result.Proposal.NewConfigID].State != "committed" {
		t.Fatalf("new config was not committed")
	}
}

func TestResolveProposalRejectsWhenApprovalBecomesImpossible(t *testing.T) {
	store := newFakeStore()
	service := NewService(store)

	newAmount := int64(15000)
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		GroupID:    "group-1",
		ProposedBy: "founder-1",
		Changes:    ConfigPatch{AmountMinor: &newAmount},
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	_, _ = service.CastVote(context.Background(), proposal.Proposal.ID, CastVoteInput{
		VoterID:  "member-1",
		Decision: VoteDecisionReject,
	})
	_, _ = service.CastVote(context.Background(), proposal.Proposal.ID, CastVoteInput{
		VoterID:  "member-2",
		Decision: VoteDecisionReject,
	})

	result, err := service.ResolveProposal(context.Background(), proposal.Proposal.ID, ResolveProposalInput{
		RequestedBy: "founder-1",
	})
	if err != nil {
		t.Fatalf("resolve proposal: %v", err)
	}
	if result.Proposal.Status != ProposalStatusRejected {
		t.Fatalf("unexpected status %s", result.Proposal.Status)
	}
}
