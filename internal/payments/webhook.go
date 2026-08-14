package payments

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

const paystackChargeSuccess = "charge.success"

// WebhookReceipt is the signed, minimal envelope persisted before work is
// attempted. PayloadHash comes from the original bytes; RawJSON deliberately
// is not retained because it is neither required for verification nor a safe
// payment source of truth.
type WebhookReceipt struct {
	Provider    string
	EventID     string
	EventType   string
	Reference   string
	PayloadHash []byte
}

type WebhookRecordResult struct {
	Duplicate      bool
	ReplayMismatch bool
}

type QueuedWebhook struct {
	ID        string
	Provider  string
	EventType string
	Reference string
	Attempts  int
}

type WebhookPaymentOwner struct {
	TenantID string
	UserID   string
}

// WebhookStore owns durable acknowledgement, exclusive claims, worker state,
// and the narrow database mapping back to a tenant-owned payment. The unique
// provider/event key in PostgreSQL, not an in-process mutex, is the webhook
// idempotency lock.
type WebhookStore interface {
	RecordWebhook(context.Context, WebhookReceipt) (WebhookRecordResult, error)
	ClaimWebhook(context.Context, string, int) (QueuedWebhook, bool, error)
	MarkWebhookProcessed(context.Context, string, bool, string) error
	MarkWebhookFailed(context.Context, QueuedWebhook, error) error
	PaymentOwnerForWebhook(context.Context, string, string) (WebhookPaymentOwner, bool, error)
}

// WebhookProcessor performs the slow, server-to-server portion after the API
// has acknowledged a signed callback. It never treats an event body as proof
// of a charge; only Service.Verify can activate a subscription.
type WebhookProcessor struct {
	store       WebhookStore
	service     *Service
	provider    string
	maxAttempts int
}

func NewWebhookProcessor(store WebhookStore, service *Service, provider string, maxAttempts int) (*WebhookProcessor, error) {
	if store == nil || service == nil || strings.TrimSpace(provider) == "" || maxAttempts < 1 {
		return nil, errors.New("payments: webhook processor requires store, service, provider, and attempts")
	}
	return &WebhookProcessor{store: store, service: service, provider: provider, maxAttempts: maxAttempts}, nil
}

// ProcessOne claims at most one ready event. Its bool result reports whether
// there was work, which keeps the worker loop simple and testable.
func (p *WebhookProcessor) ProcessOne(ctx context.Context) (bool, error) {
	if p == nil || p.store == nil || p.service == nil {
		return false, errors.New("payments: webhook processor is unavailable")
	}
	event, found, err := p.store.ClaimWebhook(ctx, p.provider, p.maxAttempts)
	if err != nil || !found {
		return found, err
	}

	if event.EventType != paystackChargeSuccess {
		return true, p.store.MarkWebhookProcessed(ctx, event.ID, true, "event type does not activate a payment")
	}
	owner, found, err := p.store.PaymentOwnerForWebhook(ctx, event.Provider, event.Reference)
	if err != nil {
		return true, p.fail(ctx, event, err)
	}
	if !found {
		// A webhook never creates a payment. The event is retained for review
		// but becomes terminal: retrying an unknown signed reference cannot
		// make it safe to activate access later.
		return true, p.store.MarkWebhookProcessed(ctx, event.ID, true, "signed event referenced no frozen payment")
	}
	_, err = p.service.Verify(ctx, owner.TenantID, owner.UserID, event.Reference)
	switch {
	case err == nil:
		return true, p.store.MarkWebhookProcessed(ctx, event.ID, false, "")
	case errors.Is(err, ErrGatewayRejected), errors.Is(err, ErrPaymentNotPending):
		return true, p.store.MarkWebhookProcessed(ctx, event.ID, true, "gateway did not confirm a pending payment")
	case errors.Is(err, ErrVerificationMismatch), errors.Is(err, ErrPaymentNotFound), errors.Is(err, ErrInvalidRequest):
		return true, p.store.MarkWebhookProcessed(ctx, event.ID, true, "payment verification facts did not match")
	default:
		return true, p.fail(ctx, event, err)
	}
}

func (p *WebhookProcessor) fail(ctx context.Context, event QueuedWebhook, err error) error {
	if err == nil {
		err = errors.New("payments: webhook processing failed")
	}
	return p.store.MarkWebhookFailed(ctx, event, err)
}

// NewWebhookReceipt derives the persistent digest before any JSON decode.
func NewWebhookReceipt(event GatewayWebhook, raw []byte) (WebhookReceipt, error) {
	if event.Provider != paystackName || event.EventID == "" || !validPaystackEventType(event.EventType) || !validReference(event.Reference) || len(raw) == 0 {
		return WebhookReceipt{}, ErrWebhookInvalid
	}
	digest := sha256.Sum256(raw)
	return WebhookReceipt{
		Provider: event.Provider, EventID: event.EventID, EventType: event.EventType,
		Reference: event.Reference, PayloadHash: digest[:],
	}, nil
}

func webhookRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	seconds := 1
	for retry := 1; retry < attempt && seconds < 300; retry++ {
		seconds *= 2
		if seconds > 300 {
			seconds = 300
		}
	}
	return time.Duration(seconds) * time.Second
}

func safeWebhookError(err error) string {
	if err == nil {
		return "webhook processing failed"
	}
	message := strings.ReplaceAll(err.Error(), "\n", " ")
	message = strings.ReplaceAll(message, "\r", " ")
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}

func webhookFailureMessage(event QueuedWebhook, err error) string {
	return fmt.Sprintf("attempt %d: %s", event.Attempts, safeWebhookError(err))
}
