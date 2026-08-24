package payments

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ReceiptEmail is the delivery-safe projection of a verified receipt event.
// It is populated by the worker's store after resolving the recipient from
// the tenant-scoped account records; no e-mail address is persisted in outbox
// payloads.
type ReceiptEmail struct {
	EventID     string
	Attempts    int
	To          string
	PlanName    string
	Reference   string
	AmountMinor int64
	Currency    string
	StartsAt    time.Time
	ExpiresAt   time.Time
}

type ReceiptStore interface {
	ClaimReceipt(context.Context, int) (ReceiptEmail, bool, error)
	MarkReceiptPublished(context.Context, string) error
	MarkReceiptFailed(context.Context, ReceiptEmail, error) error
}

type ReceiptSender interface {
	SendPaymentReceipt(context.Context, ReceiptEmail) error
}

// ReceiptProcessor delivers only after a receipt event was durably claimed.
// Delivery failure is recorded for a later retry and cannot alter payment or
// subscription state.
type ReceiptProcessor struct {
	store       ReceiptStore
	sender      ReceiptSender
	maxAttempts int
}

func NewReceiptProcessor(store ReceiptStore, sender ReceiptSender, maxAttempts int) (*ReceiptProcessor, error) {
	if store == nil || sender == nil || maxAttempts < 1 {
		return nil, errors.New("payments: receipt processor is not configured")
	}
	return &ReceiptProcessor{store: store, sender: sender, maxAttempts: maxAttempts}, nil
}

func (p *ReceiptProcessor) ProcessOne(ctx context.Context) (bool, error) {
	if p == nil || p.store == nil || p.sender == nil {
		return false, errors.New("payments: receipt processor is not configured")
	}
	receipt, found, err := p.store.ClaimReceipt(ctx, p.maxAttempts)
	if err != nil || !found {
		return false, err
	}
	if !validReceiptForDelivery(receipt) {
		if err := p.store.MarkReceiptFailed(ctx, receipt, errors.New("payments: invalid claimed receipt")); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := p.sender.SendPaymentReceipt(ctx, receipt); err != nil {
		if markErr := p.store.MarkReceiptFailed(ctx, receipt, err); markErr != nil {
			return true, markErr
		}
		return true, nil
	}
	if err := p.store.MarkReceiptPublished(ctx, receipt.EventID); err != nil {
		return true, err
	}
	return true, nil
}

func validReceiptForDelivery(receipt ReceiptEmail) bool {
	to := strings.TrimSpace(receipt.To)
	at := strings.LastIndex(to, "@")
	return validUUID(receipt.EventID) && len(to) <= 254 && at > 0 && at < len(to)-3 &&
		!strings.ContainsAny(to, " \t\r\n") && strings.Contains(to[at+1:], ".") &&
		strings.TrimSpace(receipt.PlanName) != "" && validReference(receipt.Reference) &&
		receipt.AmountMinor > 0 && sameCurrency(receipt.Currency, receipt.Currency) &&
		!receipt.StartsAt.IsZero() && !receipt.ExpiresAt.IsZero() && receipt.ExpiresAt.After(receipt.StartsAt)
}
