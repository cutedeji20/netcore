package plans

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads plans in a transaction carrying the tenant RLS setting.
// The active-subscriber aggregate retains its own tenant predicate so the
// result cannot cross a tenant boundary even if historical keys are malformed.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("plans: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, options ListOptions) (page Page, err error) {
	if tenantID == "" || options.Limit < 1 || (options.Status != "" && !IsValidStatus(options.Status)) {
		return Page{}, ErrInvalidPage
	}
	options.Search = strings.TrimSpace(options.Search)

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT p.id::text,
       p.name,
       COALESCE(p.description, ''),
       p.price_minor,
       p.currency,
       p.duration_seconds,
       p.download_bps,
       p.upload_bps,
       p.max_devices,
       p.max_concurrent_sessions,
       p.quota_bytes,
       p.quota_reset_policy,
       p.status,
       COALESCE(counts.active_subscriptions, 0),
       p.created_at,
       p.updated_at
  FROM plans AS p
  LEFT JOIN (
      SELECT plan_id, COUNT(*)::bigint AS active_subscriptions
        FROM subscriptions
       WHERE tenant_id = $1
         AND status = 'ACTIVE'
       GROUP BY plan_id
  ) AS counts
    ON counts.plan_id = p.id
 WHERE p.tenant_id = $1
   AND (
       $2 = ''
       OR p.name ILIKE '%' || $2 || '%'
       OR COALESCE(p.description, '') ILIKE '%' || $2 || '%'
   )
   AND ($3 = '' OR p.status = $3)
   AND (
       $4::timestamptz IS NULL
       OR (p.created_at, p.id) < ($4::timestamptz, $5::uuid)
   )
 ORDER BY p.created_at DESC, p.id DESC
 LIMIT $6`,
			tenantID,
			options.Search,
			string(options.Status),
			nullableCursorTime(options.Cursor),
			nullableCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query plans: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var plan Plan
			var quotaBytes pgtype.Int8
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
				&quotaBytes,
				&plan.QuotaResetPolicy,
				&plan.Status,
				&plan.ActiveSubscriptions,
				&plan.CreatedAt,
				&plan.UpdatedAt,
			); err != nil {
				return fmt.Errorf("scan plan: %w", err)
			}
			if quotaBytes.Valid {
				value := quotaBytes.Int64
				plan.QuotaBytes = &value
			}
			page.Plans = append(page.Plans, plan)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate plans: %w", err)
		}
		if len(page.Plans) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Plans = page.Plans[:options.Limit]
		last := page.Plans[len(page.Plans)-1]
		page.Next = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func nullableCursorTime(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.CreatedAt
}

func nullableCursorID(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.ID
}
