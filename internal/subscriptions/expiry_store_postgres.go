package subscriptions

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ExpireDue(ctx context.Context, tenantID string, at time.Time) (count int, err error) {
	if tenantID == "" {
		return 0, ErrUnavailable
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
WITH due AS (
  UPDATE subscriptions SET status='EXPIRED', updated_at=$2
   WHERE tenant_id=$1 AND status='ACTIVE' AND expires_at <= $2
 RETURNING id
), events AS (
  INSERT INTO subscription_events (tenant_id, subscription_id, from_status, to_status, reason, actor_type, metadata)
  SELECT $1, id, 'ACTIVE', 'EXPIRED', 'TIME_ELAPSED', 'SYSTEM', '{}'::jsonb FROM due
)
SELECT id::text FROM due`, tenantID, at)
		if err != nil {
			return fmt.Errorf("expire subscriptions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			count++
		}
		return rows.Err()
	})
	if err != nil {
		return 0, fmt.Errorf("subscriptions: expire due: %w", err)
	}
	return count, nil
}
