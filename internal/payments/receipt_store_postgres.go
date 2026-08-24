package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ClaimReceipt(ctx context.Context, maxAttempts int) (receipt ReceiptEmail, found bool, err error) {
	if s == nil || s.db == nil || maxAttempts < 1 {
		return ReceiptEmail{}, false, ErrInvalidRequest
	}
	err = s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		var paymentID, customerID, payload string
		queryErr := tx.QueryRow(ctx, `
		SELECT event_id::text, attempts, payment_id::text, customer_id::text,
       recipient_email, plan_name, reference, amount_minor, currency,
       starts_at, expires_at, payload::text
  FROM payment_receipt_claim($1)`, maxAttempts).Scan(
			&receipt.EventID,
			&receipt.Attempts,
			&paymentID,
			&customerID,
			&receipt.To,
			&receipt.PlanName,
			&receipt.Reference,
			&receipt.AmountMinor,
			&receipt.Currency,
			&receipt.StartsAt,
			&receipt.ExpiresAt,
			&payload,
		)
		if errors.Is(queryErr, pgx.ErrNoRows) {
			return nil
		}
		if queryErr != nil {
			return fmt.Errorf("payments: select receipt claim: %w", queryErr)
		}
		if !receiptPayloadMatches([]byte(payload), paymentID, customerID, receipt) {
			var rejected bool
			if err := tx.QueryRow(ctx, `SELECT payment_receipt_reject($1::uuid, $2)`, receipt.EventID, maxAttempts).Scan(&rejected); err != nil {
				return fmt.Errorf("payments: reject invalid receipt payload: %w", err)
			}
			if !rejected {
				return errors.New("payments: receipt claim was lost before payload validation")
			}
			return nil
		}
		receipt.Currency = strings.ToUpper(strings.TrimSpace(receipt.Currency))
		receipt.StartsAt = receipt.StartsAt.UTC()
		receipt.ExpiresAt = receipt.ExpiresAt.UTC()
		found = true
		return nil
	})
	return receipt, found, err
}

func (s *PostgresStore) MarkReceiptPublished(ctx context.Context, eventID string) error {
	if s == nil || s.db == nil || !validUUID(eventID) {
		return ErrInvalidRequest
	}
	return s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		var published bool
		if err := tx.QueryRow(ctx, `SELECT payment_receipt_publish($1::uuid)`, eventID).Scan(&published); err != nil {
			return fmt.Errorf("payments: publish receipt: %w", err)
		}
		if !published {
			return errors.New("payments: receipt claim was lost before publish")
		}
		return nil
	})
}

func (s *PostgresStore) MarkReceiptFailed(ctx context.Context, receipt ReceiptEmail, cause error) error {
	if s == nil || s.db == nil || !validUUID(receipt.EventID) || receipt.Attempts < 1 {
		return ErrInvalidRequest
	}
	delay := webhookRetryDelay(receipt.Attempts)
	return s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		var failed bool
		if err := tx.QueryRow(ctx, `
SELECT payment_receipt_fail($1::uuid, $2::bigint, $3)`, receipt.EventID, int64(delay.Seconds()), receiptFailureMessage(receipt, cause)).Scan(&failed); err != nil {
			return fmt.Errorf("payments: fail receipt: %w", err)
		}
		if !failed {
			return errors.New("payments: receipt claim was lost before failure recording")
		}
		return nil
	})
}

type receiptPayload struct {
	PaymentID   string    `json:"payment_id"`
	CustomerID  string    `json:"customer_id"`
	PlanName    string    `json:"plan_name"`
	Reference   string    `json:"reference"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	StartsAt    time.Time `json:"starts_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func receiptPayloadMatches(raw []byte, paymentID, customerID string, receipt ReceiptEmail) bool {
	var payload receiptPayload
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	return payload.PaymentID == paymentID && payload.CustomerID == customerID &&
		payload.PlanName == receipt.PlanName && payload.Reference == receipt.Reference &&
		payload.AmountMinor == receipt.AmountMinor && sameCurrency(payload.Currency, receipt.Currency) &&
		payload.StartsAt.Equal(receipt.StartsAt) && payload.ExpiresAt.Equal(receipt.ExpiresAt)
}

func receiptFailureMessage(receipt ReceiptEmail, err error) string {
	return fmt.Sprintf("attempt %d: %s", receipt.Attempts, safeWebhookError(err))
}
