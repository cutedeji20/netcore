package portal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore keeps the entitlement decision and handoff insert in one
// tenant-scoped transaction. It stores no raw nonce and emits no MAC or NAS
// data into the audit trail.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("portal: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

// CustomerAccount returns only data owned by the authenticated customer. The
// user and tenant predicates intentionally sit in every query even though the
// transaction is tenant-scoped, protecting this boundary from a future RLS
// configuration mistake.
func (s *PostgresStore) CustomerAccount(ctx context.Context, tenantID, userID string) (account CustomerAccount, found bool, err error) {
	if !validUUID(tenantID) || !validUUID(userID) {
		return CustomerAccount{}, false, ErrCustomerAccountUnavailable
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var customerExists bool
		err := tx.QueryRow(ctx, `
SELECT EXISTS (
SELECT 1
  FROM customers
 WHERE tenant_id = $1
   AND user_id = $2
	   AND status = 'ACTIVE'
)`, tenantID, userID).Scan(&customerExists)
		if err != nil {
			return fmt.Errorf("resolve portal customer account: %w", err)
		}
		if !customerExists {
			return nil
		}
		found = true

		subscriptions, err := tx.Query(ctx, `
SELECT plan.name,
       subscription.status,
       subscription.payment_status,
       subscription.starts_at,
       subscription.expires_at
  FROM subscriptions AS subscription
  JOIN customers AS customer
    ON customer.id = subscription.customer_id
   AND customer.tenant_id = subscription.tenant_id
  JOIN plans AS plan
    ON plan.id = subscription.plan_id
   AND plan.tenant_id = subscription.tenant_id
 WHERE subscription.tenant_id = $1
   AND customer.user_id = $2
   AND customer.status = 'ACTIVE'
 ORDER BY COALESCE(subscription.expires_at, subscription.created_at) DESC, subscription.id DESC
 LIMIT 12`, tenantID, userID)
		if err != nil {
			return fmt.Errorf("query portal subscriptions: %w", err)
		}
		defer subscriptions.Close()
		for subscriptions.Next() {
			var subscription CustomerSubscription
			var startsAt, expiresAt *time.Time
			if err := subscriptions.Scan(&subscription.PlanName, &subscription.Status, &subscription.PaymentStatus, &startsAt, &expiresAt); err != nil {
				return fmt.Errorf("scan portal subscription: %w", err)
			}
			if startsAt != nil {
				value := startsAt.UTC()
				subscription.StartsAt = &value
			}
			if expiresAt != nil {
				value := expiresAt.UTC()
				subscription.ExpiresAt = &value
			}
			account.Subscriptions = append(account.Subscriptions, subscription)
		}
		if err := subscriptions.Err(); err != nil {
			return fmt.Errorf("iterate portal subscriptions: %w", err)
		}

		payments, err := tx.Query(ctx, `
SELECT payment.provider_reference,
       payment.amount_minor,
       payment.currency,
       payment.status,
       payment.created_at
  FROM payments AS payment
  JOIN customers AS customer
    ON customer.id = payment.customer_id
   AND customer.tenant_id = payment.tenant_id
 WHERE payment.tenant_id = $1
   AND customer.user_id = $2
   AND customer.status = 'ACTIVE'
 ORDER BY payment.created_at DESC, payment.id DESC
 LIMIT 20`, tenantID, userID)
		if err != nil {
			return fmt.Errorf("query portal payments: %w", err)
		}
		defer payments.Close()
		for payments.Next() {
			var payment CustomerPayment
			if err := payments.Scan(&payment.Reference, &payment.AmountMinor, &payment.Currency, &payment.Status, &payment.CreatedAt); err != nil {
				return fmt.Errorf("scan portal payment: %w", err)
			}
			payment.CreatedAt = payment.CreatedAt.UTC()
			account.Payments = append(account.Payments, payment)
		}
		if err := payments.Err(); err != nil {
			return fmt.Errorf("iterate portal payments: %w", err)
		}
		return nil
	})
	return account, found, err
}

func (s *PostgresStore) IssueHandoff(ctx context.Context, record HandoffRecord) error {
	if record.TenantID == "" || record.UserID == "" || record.ClientMAC == "" || record.NASAddress == "" || len(record.TokenHash) != 32 || record.ExpiresAt.IsZero() {
		return ErrInvalidContext
	}

	return s.db.InTenantTx(ctx, record.TenantID, func(tx pgx.Tx) error {
		var nasID string
		err := tx.QueryRow(ctx, `
SELECT id::text
  FROM nas
 WHERE tenant_id = $1
   AND nasname = $2::inet
   AND status = 'ACTIVE'`,
			record.TenantID,
			record.NASAddress,
		).Scan(&nasID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidContext
		}
		if err != nil {
			return fmt.Errorf("resolve portal NAS: %w", err)
		}

		var subscriptionID string
		err = tx.QueryRow(ctx, `
SELECT subscription.id::text
  FROM customers AS customer
  JOIN subscriptions AS subscription
    ON subscription.customer_id = customer.id
   AND subscription.tenant_id = customer.tenant_id
 WHERE customer.tenant_id = $1
   AND customer.user_id = $2
   AND subscription.status = 'ACTIVE'
   AND subscription.starts_at <= now()
   AND subscription.expires_at > now()
 ORDER BY subscription.expires_at ASC, subscription.id ASC
 LIMIT 1`,
			record.TenantID,
			record.UserID,
		).Scan(&subscriptionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoActivePlan
		}
		if err != nil {
			return fmt.Errorf("resolve portal entitlement: %w", err)
		}

		var handoffID string
		err = tx.QueryRow(ctx, `
INSERT INTO portal_handoffs (
    tenant_id, subscription_id, nas_id, user_id, client_mac, nonce_hash, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id::text`,
			record.TenantID,
			subscriptionID,
			nasID,
			record.UserID,
			record.ClientMAC,
			record.TokenHash,
			record.ExpiresAt,
		).Scan(&handoffID)
		if err != nil {
			return fmt.Errorf("insert portal handoff: %w", err)
		}

		if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id)
VALUES ($1, 'USER', $2, 'PORTAL_HANDOFF_ISSUED', 'portal_handoff', $3)`,
			record.TenantID,
			record.UserID,
			handoffID,
		); err != nil {
			return fmt.Errorf("write portal handoff audit record: %w", err)
		}
		return nil
	})
}

// ResolveTenant resolves the deployment-selected portal tenant before a
// public catalogue query. The caller never receives its internal UUID.
func (s *PostgresStore) ResolveTenant(ctx context.Context, slug string) (string, bool, error) {
	tenant, found, err := s.db.ResolveActiveTenant(ctx, slug)
	if err != nil {
		return "", false, fmt.Errorf("resolve portal tenant: %w", err)
	}
	return tenant.ID, found, nil
}

// ListPublishedPlans selects only active plan records and maps them straight
// to the public allow-list model. Draft/retired and operational fields cannot
// leak through this query.
func (s *PostgresStore) ListPublishedPlans(ctx context.Context, tenantID string) (plans []PublicPlan, err error) {
	if tenantID == "" {
		return nil, ErrCatalogueUnavailable
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT id::text,
       name,
       COALESCE(description, ''),
       price_minor,
       currency,
       duration_seconds,
       download_bps,
       upload_bps,
       max_devices,
       max_concurrent_sessions
  FROM plans
 WHERE tenant_id = $1
   AND status = 'ACTIVE'
 ORDER BY priority ASC, name ASC, id ASC`, tenantID)
		if err != nil {
			return fmt.Errorf("query published portal plans: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var plan PublicPlan
			if err := rows.Scan(
				&plan.ID,
				&plan.Name,
				&plan.Description,
				&plan.PriceMinor,
				&plan.Currency,
				&plan.DurationSeconds,
				&plan.DownloadBPS,
				&plan.UploadBPS,
				&plan.MaxDevices,
				&plan.MaxConcurrentSessions,
			); err != nil {
				return fmt.Errorf("scan published portal plan: %w", err)
			}
			plans = append(plans, plan)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate published portal plans: %w", err)
		}
		return nil
	})
	return plans, err
}
