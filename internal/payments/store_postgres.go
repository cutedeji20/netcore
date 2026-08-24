package payments

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore preserves the payment boundary in the database: price lookup,
// pending record, activation event, quota period, outbox records and audit
// record all run under the same tenant transaction where applicable.
type PostgresStore struct {
	db             *database.Pool
	receiptEnabled bool
}

func NewPostgresStore(db *database.Pool, receiptEnabled ...bool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("payments: database pool is required")
	}
	store := &PostgresStore{db: db}
	if len(receiptEnabled) > 0 {
		store.receiptEnabled = receiptEnabled[0]
	}
	return store, nil
}

func (s *PostgresStore) PrepareInitiation(ctx context.Context, input Initiation) (pending PendingPayment, replay *Checkout, err error) {
	if !validUUID(input.TenantID) || !validUUID(input.UserID) || !validUUID(input.PlanID) || strings.TrimSpace(input.Gateway) == "" || !validReference(input.Reference) || !validIdempotencyKey(input.IdempotencyKey) || len(input.RequestHash) != 32 {
		return PendingPayment{}, nil, ErrInvalidRequest
	}
	err = s.db.InTenantTx(ctx, input.TenantID, func(tx pgx.Tx) error {
		var storedUserID pgtype.Text
		var storedHash []byte
		var storedStatus pgtype.Int4
		var storedBody string
		lookupErr := tx.QueryRow(ctx, `
SELECT user_id::text, request_hash, response_status, COALESCE(response_body::text, '{}')
  FROM idempotency_keys
 WHERE tenant_id = $1 AND endpoint = 'POST /api/v1/payments' AND key = $2
 FOR UPDATE`, input.TenantID, input.IdempotencyKey).Scan(&storedUserID, &storedHash, &storedStatus, &storedBody)
		switch {
		case lookupErr == nil:
			if !storedUserID.Valid || storedUserID.String != input.UserID || len(storedHash) != len(input.RequestHash) || subtle.ConstantTimeCompare(storedHash, input.RequestHash) != 1 {
				return ErrIdempotencyConflict
			}
			if storedStatus.Valid {
				var checkout Checkout
				if err := json.Unmarshal([]byte(storedBody), &checkout); err != nil || !validReference(checkout.Reference) || !validCheckoutURL(checkout.AuthorizationURL) {
					return fmt.Errorf("payments: corrupt completed idempotency response")
				}
				replay = &checkout
				return nil
			}
			var staging struct {
				Reference string `json:"provider_reference"`
			}
			if err := json.Unmarshal([]byte(storedBody), &staging); err != nil || !validReference(staging.Reference) {
				return fmt.Errorf("payments: corrupt pending idempotency response")
			}
			pending, err = loadPending(ctx, tx, input.TenantID, input.UserID, input.Gateway, staging.Reference)
			return err
		case errors.Is(lookupErr, pgx.ErrNoRows):
			// Continue below. The INSERT is guarded by the unique key; a racing
			// request retries through the same idempotency path.
		default:
			return fmt.Errorf("payments: read idempotency key: %w", lookupErr)
		}

		var customerID, customerEmail, currency string
		var amountMinor int64
		planErr := tx.QueryRow(ctx, `
SELECT c.id::text,
       COALESCE(NULLIF(c.email::text, ''), u.email::text, ''),
       p.price_minor,
       p.currency
  FROM customers AS c
  JOIN users AS u ON u.id = c.user_id AND u.tenant_id = c.tenant_id
  JOIN plans AS p ON p.tenant_id = c.tenant_id
 WHERE c.tenant_id = $1
   AND c.user_id = $2
   AND c.status = 'ACTIVE'
   AND p.id = $3
   AND p.status = 'ACTIVE'
   AND p.price_minor > 0`, input.TenantID, input.UserID, input.PlanID).Scan(&customerID, &customerEmail, &amountMinor, &currency)
		if errors.Is(planErr, pgx.ErrNoRows) {
			return ErrPaymentNotFound
		}
		if planErr != nil {
			return fmt.Errorf("payments: resolve customer plan: %w", planErr)
		}
		if strings.TrimSpace(customerEmail) == "" {
			return ErrInvalidRequest
		}

		var subscriptionID string
		if err := tx.QueryRow(ctx, `
INSERT INTO subscriptions (tenant_id, customer_id, plan_id, status, payment_status)
VALUES ($1, $2, $3, 'PENDING', 'UNPAID')
RETURNING id::text`, input.TenantID, customerID, input.PlanID).Scan(&subscriptionID); err != nil {
			return fmt.Errorf("payments: create pending subscription: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO subscription_events
    (tenant_id, subscription_id, from_status, to_status, reason, actor_type, actor_id, metadata)
VALUES ($1, $2, NULL, 'PENDING', 'PAYMENT', 'CUSTOMER', $3, '{}'::jsonb)`, input.TenantID, subscriptionID, input.UserID); err != nil {
			return fmt.Errorf("payments: record pending subscription: %w", err)
		}
		var paymentID string
		if err := tx.QueryRow(ctx, `
INSERT INTO payments
    (tenant_id, customer_id, subscription_id, gateway, provider_reference, amount_minor, currency, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'PENDING')
RETURNING id::text`, input.TenantID, customerID, subscriptionID, input.Gateway, input.Reference, amountMinor, strings.ToUpper(currency)).Scan(&paymentID); err != nil {
			return fmt.Errorf("payments: create pending payment: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO idempotency_keys
    (tenant_id, user_id, endpoint, key, request_hash, response_body, expires_at)
VALUES ($1, $2, 'POST /api/v1/payments', $3, $4,
        jsonb_build_object('provider_reference', $5), now() + interval '24 hours')`,
			input.TenantID, input.UserID, input.IdempotencyKey, input.RequestHash, input.Reference); err != nil {
			// A concurrent writer has won this key. Rolling back prevents a
			// second pending subscription and lets the browser safely retry.
			return fmt.Errorf("payments: reserve idempotency key: %w", err)
		}
		pending = PendingPayment{ID: paymentID, SubscriptionID: subscriptionID, Reference: input.Reference, AmountMinor: amountMinor, Currency: strings.ToUpper(currency), CustomerEmail: customerEmail}
		return nil
	})
	if isIdempotencyConflict(err) {
		// The row that won the unique-key race is now visible. Re-entering the
		// locked read path returns its pending or completed checkout without
		// creating a second subscription.
		return s.PrepareInitiation(ctx, input)
	}
	return pending, replay, err
}

func isIdempotencyConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idempotency_keys_tenant_id_key_endpoint_key"
}

func loadPending(ctx context.Context, tx pgx.Tx, tenantID, userID, gateway, reference string) (PendingPayment, error) {
	var pending PendingPayment
	err := tx.QueryRow(ctx, `
SELECT p.id::text, p.subscription_id::text, p.provider_reference, p.amount_minor, p.currency,
       COALESCE(NULLIF(c.email::text, ''), u.email::text, '')
  FROM payments AS p
  JOIN customers AS c ON c.id = p.customer_id AND c.tenant_id = p.tenant_id
  JOIN users AS u ON u.id = c.user_id AND u.tenant_id = c.tenant_id
 WHERE p.tenant_id = $1
   AND c.user_id = $2
   AND p.gateway = $3
   AND p.provider_reference = $4
   AND p.status = 'PENDING'`, tenantID, userID, gateway, reference).Scan(
		&pending.ID, &pending.SubscriptionID, &pending.Reference, &pending.AmountMinor, &pending.Currency, &pending.CustomerEmail,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingPayment{}, ErrPaymentNotPending
	}
	if err != nil {
		return PendingPayment{}, fmt.Errorf("payments: load pending payment: %w", err)
	}
	return pending, nil
}

func (s *PostgresStore) SaveCheckout(ctx context.Context, tenantID, idempotencyKey string, checkout Checkout) error {
	if !validUUID(tenantID) || !validIdempotencyKey(idempotencyKey) || !validReference(checkout.Reference) || !validCheckoutURL(checkout.AuthorizationURL) {
		return ErrInvalidRequest
	}
	body, err := json.Marshal(checkout)
	if err != nil {
		return fmt.Errorf("payments: encode checkout: %w", err)
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
UPDATE idempotency_keys
   SET response_status = 201,
       response_body = $4::jsonb
 WHERE tenant_id = $1
   AND endpoint = 'POST /api/v1/payments'
   AND key = $2
   AND response_status IS NULL
   AND response_body ->> 'provider_reference' = $3`,
			tenantID, idempotencyKey, checkout.Reference, string(body))
		if err != nil {
			return fmt.Errorf("payments: save checkout response: %w", err)
		}
		return nil
	})
}

func (s *PostgresStore) PaymentForVerification(ctx context.Context, tenantID, userID, reference string) (Payment, error) {
	var payment Payment
	if !validUUID(tenantID) || !validUUID(userID) || !validReference(reference) {
		return Payment{}, ErrInvalidRequest
	}
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
SELECT p.id::text, p.subscription_id::text, p.gateway, p.provider_reference,
       p.amount_minor, p.currency, p.status
  FROM payments AS p
  JOIN customers AS c ON c.id = p.customer_id AND c.tenant_id = p.tenant_id
 WHERE p.tenant_id = $1
   AND c.user_id = $2
   AND p.provider_reference = $3`, tenantID, userID, reference).Scan(
			&payment.ID, &payment.SubscriptionID, &payment.Gateway, &payment.Reference,
			&payment.AmountMinor, &payment.Currency, &payment.Status,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPaymentNotFound
		}
		if err != nil {
			return fmt.Errorf("payments: load verification payment: %w", err)
		}
		return nil
	})
	if err != nil {
		return Payment{}, err
	}
	return payment, nil
}

// ActivateVerified is the §52 transaction.  No network call is made here:
// by the time this code runs, the gateway response has already been checked.
func (s *PostgresStore) ActivateVerified(ctx context.Context, input Activation) (result ActivationResult, err error) {
	if !validUUID(input.TenantID) || !validUUID(input.UserID) || strings.TrimSpace(input.Gateway) == "" || !validReference(input.Reference) || input.AmountMinor <= 0 || !sameCurrency(input.Currency, input.Currency) || input.VerifiedAt.IsZero() {
		return ActivationResult{}, ErrInvalidRequest
	}
	err = s.db.InTenantTx(ctx, input.TenantID, func(tx pgx.Tx) error {
		var paymentID, subscriptionID, customerID, planID, planName, paymentStatus, subscriptionStatus, currency string
		var amountMinor, quotaBytes int64
		var durationSeconds int64
		lookupErr := tx.QueryRow(ctx, `
		SELECT p.id::text, p.subscription_id::text, p.status, p.amount_minor, p.currency,
	       s.customer_id::text, s.plan_id::text, s.status, plan.name,
	       plan.duration_seconds, COALESCE(plan.quota_bytes, 0)
  FROM payments AS p
  JOIN subscriptions AS s ON s.id = p.subscription_id AND s.tenant_id = p.tenant_id
  JOIN customers AS c ON c.id = p.customer_id AND c.tenant_id = p.tenant_id
  JOIN plans AS plan ON plan.id = s.plan_id AND plan.tenant_id = s.tenant_id
 WHERE p.tenant_id = $1
   AND c.user_id = $2
   AND p.gateway = $3
   AND p.provider_reference = $4
 FOR UPDATE OF p, s`, input.TenantID, input.UserID, input.Gateway, input.Reference).Scan(
			&paymentID, &subscriptionID, &paymentStatus, &amountMinor, &currency,
			&customerID, &planID, &subscriptionStatus, &planName, &durationSeconds, &quotaBytes,
		)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return ErrPaymentNotFound
		}
		if lookupErr != nil {
			return fmt.Errorf("payments: lock activation payment: %w", lookupErr)
		}

		if paymentStatus == StatusSuccess {
			var startsAt, expiresAt time.Time
			if err := tx.QueryRow(ctx, `SELECT starts_at, expires_at FROM subscriptions WHERE id = $1`, subscriptionID).Scan(&startsAt, &expiresAt); err != nil {
				return fmt.Errorf("payments: read activated subscription: %w", err)
			}
			result = ActivationResult{PaymentID: paymentID, SubscriptionID: subscriptionID, Status: StatusSuccess, StartsAt: startsAt.UTC(), ExpiresAt: expiresAt.UTC(), AlreadyActivated: true}
			return nil
		}
		if paymentStatus != StatusPending || subscriptionStatus != "PENDING" {
			return ErrPaymentNotPending
		}
		if amountMinor != input.AmountMinor || !sameCurrency(currency, input.Currency) {
			return ErrVerificationMismatch
		}

		var startsAt, expiresAt time.Time
		if err := tx.QueryRow(ctx, `
UPDATE subscriptions
   SET status = 'ACTIVE', payment_status = 'PAID', starts_at = now(),
       expires_at = now() + $2 * interval '1 second', updated_at = now()
 WHERE id = $1 AND status = 'PENDING'
RETURNING starts_at, expires_at`, subscriptionID, durationSeconds).Scan(&startsAt, &expiresAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPaymentNotPending
			}
			return fmt.Errorf("payments: activate subscription: %w", err)
		}
		var verifiedAt time.Time
		if err := tx.QueryRow(ctx, `
UPDATE payments
   SET status = 'SUCCESS', verified_at = now(), updated_at = now()
 WHERE id = $1 AND status = 'PENDING'
RETURNING verified_at`, paymentID).Scan(&verifiedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPaymentNotPending
			}
			return fmt.Errorf("payments: record verified payment: %w", err)
		}
		var subscriptionEventID string
		if err := tx.QueryRow(ctx, `
INSERT INTO subscription_events
    (tenant_id, subscription_id, from_status, to_status, reason, actor_type, metadata)
VALUES ($1, $2, 'PENDING', 'ACTIVE', 'PAYMENT', 'GATEWAY',
        jsonb_build_object('payment_id', $3::uuid, 'verification_source', 'server_to_server'))
RETURNING id::text`, input.TenantID, subscriptionID, paymentID).Scan(&subscriptionEventID); err != nil {
			return fmt.Errorf("payments: write activation event: %w", err)
		}

		// ACTIVE always gets a current usage period. For an unmetered plan the
		// zero quota row satisfies the state invariant but accounting never
		// calls quota_apply for that plan.
		if _, err := tx.Exec(ctx, `
INSERT INTO usage_counters
    (tenant_id, subscription_id, customer_id, period_start, period_end, quota_bytes)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (subscription_id, period_start) DO NOTHING`,
			input.TenantID, subscriptionID, customerID, startsAt, expiresAt, quotaBytes); err != nil {
			return fmt.Errorf("payments: create usage period: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO outbox_events
    (event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload)
VALUES ($1::uuid, $2, 'subscription', $3::uuid, 'subscription.activated',
        jsonb_build_object('subscription_id', $3::uuid, 'customer_id', $4::uuid,
            'plan_id', $5::uuid, 'previous_status', 'PENDING',
            'activation_reason', 'PAYMENT', 'period_start', $6,
            'period_end', $7, 'usage_counter_period_start', $6,
            'quota_bytes', $8, 'payment_id', $9::uuid,
            'actor_type', 'GATEWAY', 'actor_id', NULL))`,
			deterministicEventID("subscription.activated", subscriptionEventID), input.TenantID,
			subscriptionID, customerID, planID, startsAt, expiresAt, quotaBytes, paymentID); err != nil {
			return fmt.Errorf("payments: queue subscription activation: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO outbox_events
    (event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload)
VALUES ($1::uuid, $2, 'payment', $3::uuid, 'payment.succeeded',
        jsonb_build_object('payment_id', $3::uuid, 'gateway', $4,
            'provider_reference', $5, 'customer_id', $6::uuid,
            'subscription_id', $7::uuid, 'amount_minor', $8,
            'currency', $9, 'verified_at', $10,
            'verification_source', 'server_to_server'))`,
			deterministicEventID("payment.succeeded", input.Gateway, input.Reference), input.TenantID,
			paymentID, input.Gateway, input.Reference, customerID, subscriptionID, amountMinor, currency, verifiedAt); err != nil {
			return fmt.Errorf("payments: queue payment success: %w", err)
		}
		if s.receiptEnabled {
			receipt, err := NewReceiptEvent(paymentID, customerID, planName, input.Reference, amountMinor, currency, startsAt, expiresAt)
			if err != nil {
				return fmt.Errorf("payments: build receipt event: %w", err)
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO outbox_events
    (event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload)
VALUES ($1::uuid, $2, 'payment', $3::uuid, 'payment.receipt.requested',
        jsonb_build_object('payment_id', $3::uuid, 'customer_id', $4::uuid,
            'plan_name', $5, 'reference', $6, 'amount_minor', $7,
            'currency', $8, 'starts_at', $9, 'expires_at', $10))
ON CONFLICT (event_id) DO NOTHING`,
				receipt.EventID, input.TenantID, receipt.PaymentID, receipt.CustomerID,
				receipt.PlanName, receipt.Reference, receipt.AmountMinor, receipt.Currency,
				receipt.StartsAt, receipt.ExpiresAt); err != nil {
				return fmt.Errorf("payments: queue payment receipt: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, action, resource_type, resource_id, metadata)
VALUES ($1, 'GATEWAY', 'PAYMENT_VERIFIED', 'payment', $2::uuid,
        jsonb_build_object('verification_source', 'server_to_server'))`, input.TenantID, paymentID); err != nil {
			return fmt.Errorf("payments: write payment audit record: %w", err)
		}
		result = ActivationResult{PaymentID: paymentID, SubscriptionID: subscriptionID, Status: StatusSuccess, StartsAt: startsAt.UTC(), ExpiresAt: expiresAt.UTC()}
		return nil
	})
	if err != nil {
		return ActivationResult{}, err
	}
	return result, nil
}

// deterministicEventID is UUIDv5-compatible: a reproducible version-5 UUID
// gives consumers the same id across retries without relying on wall-clock
// randomness. The fixed namespace identifies NetCore's event domain.
func deterministicEventID(parts ...string) string {
	namespace := [16]byte{0x6d, 0xfc, 0x4b, 0x8d, 0x31, 0x33, 0x50, 0x7b, 0x8a, 0x96, 0x44, 0x6e, 0x29, 0x7b, 0x0b, 0x61}
	h := sha1.New()
	_, _ = h.Write(namespace[:])
	_, _ = h.Write([]byte(strings.Join(parts, "|")))
	value := h.Sum(nil)[:16]
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}
