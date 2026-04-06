// Package notification — Dispatcher Outbox Worker
//
// Ce fichier branche le service de notification réel sur l'outbox worker.
// Il remplace le stub NotificationDispatcher de outbox/main.go.
//
// INTÉGRATION : dans outbox/main.go, remplacer NewNotificationDispatcher(log)
// par notification.NewOutboxDispatcher(cfg, log).
package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"masecure/internal/outboxworker"
)

// OutboxDispatcher implémente outboxworker.Dispatcher pour les notifications.
type OutboxDispatcher struct {
	sender *Sender
	log    *zap.Logger
}

// NewOutboxDispatcher crée un dispatcher de notifications branché sur l'Outbox Worker.
func NewOutboxDispatcher(config Config, log *zap.Logger) *OutboxDispatcher {
	return &OutboxDispatcher{
		sender: NewSender(config, log),
		log:    log,
	}
}

func (d *OutboxDispatcher) CanHandle(target string) bool {
	return target == "notification-svc"
}

// Dispatch route un événement de domaine vers le bon message de notification.
func (d *OutboxDispatcher) Dispatch(ctx context.Context, row outboxworker.OutboxRow) (string, error) {
	d.log.Info("Dispatching notification",
		zap.String("event_type", row.EventType),
		zap.String("aggregate_id", row.AggregateID.String()),
	)

	var req NotificationRequest
	var err error

	switch row.EventType {
	case "contribution.received":
		req, err = d.buildContributionReceived(row.Payload)
	case "payout.confirmed":
		req, err = d.buildPayoutConfirmed(row.Payload)
	case "payout.failed":
		req, err = d.buildPayoutFailed(row.Payload)
	case "contribution.quarantined":
		req, err = d.buildQuarantineAlert(row.Payload)
	case "debt.created":
		req, err = d.buildDebtNotification(row.Payload)
	case "governance.proposal_created":
		req, err = d.buildProposalNotification(row.Payload)
	case "pro_rata.dispatched":
		req, err = d.buildProRataNotice(row.Payload)
	default:
		d.log.Info("No notification template for event type", zap.String("event_type", row.EventType))
		return "skipped", nil
	}

	if err != nil {
		return "", fmt.Errorf("build notification for %s: %w", row.EventType, err)
	}

	req.EventType = row.EventType
	req.GroupID = row.AggregateID.String()

	result := d.sender.Send(ctx, req)
	if result.Error != nil {
		return "", result.Error
	}

	ref := fmt.Sprintf("notif:%s:%s", result.Channel, result.MessageID)
	return ref, nil
}

// ── Builders d'événements → NotificationRequest ───────────────────────────────

func (d *OutboxDispatcher) buildContributionReceived(payload json.RawMessage) (NotificationRequest, error) {
	var evt struct {
		CycleID       string `json:"cycle_id"`
		PayerMsisdn   string `json:"payer_msisdn"`
		AmountMinor   int64  `json:"amount_minor"`
		CycleNumber   int    `json:"cycle_number"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return NotificationRequest{}, err
	}
	return BuildContributionConfirmed(evt.PayerMsisdn, evt.AmountMinor, evt.CycleNumber), nil
}

func (d *OutboxDispatcher) buildPayoutConfirmed(payload json.RawMessage) (NotificationRequest, error) {
	var evt struct {
		CycleID         string `json:"cycle_id"`
		BeneficiaryMsisdn string `json:"beneficiary_msisdn"`
		AmountMinor     int64  `json:"amount_minor"`
		GroupName       string `json:"group_name"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return NotificationRequest{}, err
	}
	groupName := evt.GroupName
	if groupName == "" {
		groupName = "votre tontine"
	}
	return BuildPayoutConfirmed(evt.BeneficiaryMsisdn, groupName, evt.AmountMinor), nil
}

func (d *OutboxDispatcher) buildPayoutFailed(payload json.RawMessage) (NotificationRequest, error) {
	var evt struct {
		CycleID     string `json:"cycle_id"`
		BeneficiaryMsisdn string `json:"beneficiary_msisdn"`
		CycleNumber int    `json:"cycle_number"`
		Reason      string `json:"reason"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return NotificationRequest{}, err
	}
	return BuildPayoutFailed(evt.BeneficiaryMsisdn, evt.CycleNumber), nil
}

func (d *OutboxDispatcher) buildQuarantineAlert(payload json.RawMessage) (NotificationRequest, error) {
	var evt struct {
		CycleID     string `json:"cycle_id"`
		PayerMsisdn string `json:"payer_msisdn"`
		AmountMinor int64  `json:"amount_minor"`
		Reason      string `json:"reason"`
		// MSISDN du fondateur ou administrateur du groupe — à enrichir depuis BDD
		AdminMsisdn string `json:"admin_msisdn"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return NotificationRequest{}, err
	}
	if evt.AdminMsisdn == "" {
		d.log.Warn("Quarantine alert: no admin msisdn in payload, skipping",
			zap.String("cycle_id", evt.CycleID))
		return NotificationRequest{}, fmt.Errorf("no admin msisdn for quarantine alert")
	}
	return NotificationRequest{
		RecipientMsisdn: evt.AdminMsisdn,
		Template:        TemplatePeQuarantineAlert,
		Params: []string{
			fmt.Sprintf("%d", evt.AmountMinor/100),
			evt.PayerMsisdn,
		},
		SMSBody: fmt.Sprintf(
			"MaSecure ALERTE : paiement de %d XOF reçu d'un numéro non lié (%s). Vérification requise.",
			evt.AmountMinor/100, evt.PayerMsisdn),
	}, nil
}

func (d *OutboxDispatcher) buildDebtNotification(payload json.RawMessage) (NotificationRequest, error) {
	var evt struct {
		DebtorMsisdn string `json:"debtor_msisdn"`
		CycleNumber  int    `json:"cycle_number"`
		AmountMinor  int64  `json:"amount_minor"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return NotificationRequest{}, err
	}
	return BuildDebtCreated(evt.DebtorMsisdn, evt.CycleNumber, evt.AmountMinor), nil
}

func (d *OutboxDispatcher) buildProposalNotification(payload json.RawMessage) (NotificationRequest, error) {
	var evt struct {
		MemberMsisdn string    `json:"member_msisdn"`
		ProposerName string    `json:"proposer_name"`
		ExpiresAt    time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return NotificationRequest{}, err
	}
	return BuildProposalCreated(evt.MemberMsisdn, evt.ProposerName, evt.ExpiresAt), nil
}

func (d *OutboxDispatcher) buildProRataNotice(payload json.RawMessage) (NotificationRequest, error) {
	var evt struct {
		BeneficiaryMsisdn string `json:"beneficiary_msisdn"`
		DistributableMinor int64 `json:"distributable_minor"`
		FractionPct        int   `json:"fraction_pct"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return NotificationRequest{}, err
	}
	return NotificationRequest{
		RecipientMsisdn: evt.BeneficiaryMsisdn,
		Template:        TemplatePeProRataNotice,
		Params: []string{
			fmt.Sprintf("%d", evt.DistributableMinor/100),
			fmt.Sprintf("%d%%", evt.FractionPct),
		},
		SMSBody: fmt.Sprintf(
			"MaSecure : versement partiel de %d XOF (%d%% du montant attendu). Le solde sera régularisé.",
			evt.DistributableMinor/100, evt.FractionPct),
	}, nil
}
