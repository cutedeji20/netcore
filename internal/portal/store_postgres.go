package portal

import (
	"context"
	"errors"
	"fmt"

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
