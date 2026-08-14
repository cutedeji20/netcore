package payments

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) RecordWebhook(ctx context.Context, receipt WebhookReceipt) (result WebhookRecordResult, err error) {
	if receipt.Provider != paystackName || receipt.EventID == "" || !validPaystackEventType(receipt.EventType) || !validReference(receipt.Reference) || len(receipt.PayloadHash) != 32 {
		return WebhookRecordResult{}, ErrWebhookInvalid
	}
	err = s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		var eventID string
		insertErr := tx.QueryRow(ctx, `
INSERT INTO webhook_events
    (provider, event_id, event_type, provider_reference, payload_hash, signature_valid, status)
VALUES ($1, $2, $3, $4, $5, true, 'RECEIVED')
ON CONFLICT (provider, event_id) DO NOTHING
RETURNING id::text`, receipt.Provider, receipt.EventID, receipt.EventType, receipt.Reference, receipt.PayloadHash).Scan(&eventID)
		switch {
		case insertErr == nil:
			return nil
		case !errors.Is(insertErr, pgx.ErrNoRows):
			return fmt.Errorf("payments: record webhook: %w", insertErr)
		}

		var existingID string
		var existingHash []byte
		if err := tx.QueryRow(ctx, `
SELECT id::text, payload_hash
  FROM webhook_events
 WHERE provider = $1 AND event_id = $2`, receipt.Provider, receipt.EventID).Scan(&existingID, &existingHash); err != nil {
			return fmt.Errorf("payments: read existing webhook: %w", err)
		}
		if len(existingHash) == len(receipt.PayloadHash) && subtle.ConstantTimeCompare(existingHash, receipt.PayloadHash) == 1 {
			result.Duplicate = true
			return nil
		}
		// Same provider event identity with a different signed raw body is not a
		// retry. Persist a non-sensitive security audit fact and do not let it
		// alter the event already claimed by another worker.
		if _, err := tx.Exec(ctx, `
SELECT payment_webhook_audit($1::uuid, 'WEBHOOK_REPLAY_DETECTED',
       jsonb_build_object('provider', $2, 'payload_hash', $3))`, existingID, receipt.Provider, hex.EncodeToString(receipt.PayloadHash)); err != nil {
			return fmt.Errorf("payments: audit webhook replay: %w", err)
		}
		result.Duplicate = true
		result.ReplayMismatch = true
		return nil
	})
	return result, err
}

func (s *PostgresStore) ClaimWebhook(ctx context.Context, provider string, maxAttempts int) (event QueuedWebhook, found bool, err error) {
	if provider != paystackName || maxAttempts < 1 {
		return QueuedWebhook{}, false, ErrInvalidRequest
	}
	err = s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		// A worker crash must not leave a legitimate, signed event permanently
		// stranded. Reclaim only after a long bounded lease, not immediately.
		if _, err := tx.Exec(ctx, `
UPDATE webhook_events
   SET status = 'FAILED', processing_started_at = NULL, next_attempt_at = now(),
       last_error = 'worker claim lease expired'
 WHERE provider = $1
   AND status = 'PROCESSING'
   AND processing_started_at < now() - interval '5 minutes'`, provider); err != nil {
			return fmt.Errorf("payments: recover webhook claims: %w", err)
		}

		var id string
		queryErr := tx.QueryRow(ctx, `
SELECT id::text, provider, event_type, provider_reference, attempts
  FROM webhook_events
 WHERE provider = $1
   AND status IN ('RECEIVED', 'FAILED')
   AND next_attempt_at <= now()
   AND attempts < $2
   AND provider_reference IS NOT NULL
 ORDER BY next_attempt_at, received_at, id
 FOR UPDATE SKIP LOCKED
 LIMIT 1`, provider, maxAttempts).Scan(&id, &event.Provider, &event.EventType, &event.Reference, &event.Attempts)
		if errors.Is(queryErr, pgx.ErrNoRows) {
			return nil
		}
		if queryErr != nil {
			return fmt.Errorf("payments: select webhook claim: %w", queryErr)
		}
		if err := tx.QueryRow(ctx, `
UPDATE webhook_events
   SET status = 'PROCESSING', attempts = attempts + 1,
       processing_started_at = now(), last_error = NULL
 WHERE id = $1::uuid
RETURNING attempts`, id).Scan(&event.Attempts); err != nil {
			return fmt.Errorf("payments: claim webhook: %w", err)
		}
		event.ID = id
		found = true
		return nil
	})
	return event, found, err
}

func (s *PostgresStore) MarkWebhookProcessed(ctx context.Context, id string, ignored bool, reason string) error {
	if !validUUID(id) {
		return ErrInvalidRequest
	}
	return s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		status := "PROCESSED"
		if ignored {
			status = "IGNORED"
		}
		result, err := tx.Exec(ctx, `
UPDATE webhook_events
   SET status = $2, processed_at = now(), processing_started_at = NULL,
       last_error = NULL
 WHERE id = $1::uuid AND status = 'PROCESSING'`, id, status)
		if err != nil {
			return fmt.Errorf("payments: complete webhook: %w", err)
		}
		if result.RowsAffected() != 1 {
			return errors.New("payments: webhook claim was lost before completion")
		}
		if ignored {
			if _, err := tx.Exec(ctx, `
SELECT payment_webhook_audit($1::uuid, 'PAYMENT_WEBHOOK_IGNORED',
       jsonb_build_object('reason', $2))`, id, safeWebhookError(errors.New(reason))); err != nil {
				return fmt.Errorf("payments: audit ignored webhook: %w", err)
			}
		}
		return nil
	})
}

func (s *PostgresStore) MarkWebhookFailed(ctx context.Context, event QueuedWebhook, cause error) error {
	if !validUUID(event.ID) || event.Attempts < 1 {
		return ErrInvalidRequest
	}
	delay := webhookRetryDelay(event.Attempts)
	return s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `
UPDATE webhook_events
   SET status = 'FAILED', processing_started_at = NULL,
       next_attempt_at = now() + $2::bigint * interval '1 second',
       last_error = $3
 WHERE id = $1::uuid AND status = 'PROCESSING'`, event.ID, int64(delay.Seconds()), webhookFailureMessage(event, cause))
		if err != nil {
			return fmt.Errorf("payments: fail webhook: %w", err)
		}
		if result.RowsAffected() != 1 {
			return errors.New("payments: webhook claim was lost before failure recording")
		}
		return nil
	})
}

func (s *PostgresStore) PaymentOwnerForWebhook(ctx context.Context, gateway, reference string) (owner WebhookPaymentOwner, found bool, err error) {
	if gateway != paystackName || !validReference(reference) {
		return WebhookPaymentOwner{}, false, ErrInvalidRequest
	}
	err = s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		queryErr := tx.QueryRow(ctx, `
SELECT tenant_id::text, user_id::text
  FROM payment_webhook_owner($1, $2)`, gateway, reference).Scan(&owner.TenantID, &owner.UserID)
		if errors.Is(queryErr, pgx.ErrNoRows) {
			return nil
		}
		if queryErr != nil {
			return fmt.Errorf("payments: resolve webhook payment owner: %w", queryErr)
		}
		if !validUUID(owner.TenantID) || !validUUID(owner.UserID) {
			return errors.New("payments: invalid webhook payment owner")
		}
		found = true
		return nil
	})
	return owner, found, err
}
