package vouchers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore groups voucher rows into browser-safe inventory batches inside
// the transaction-scoped tenant RLS boundary.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("vouchers: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, options ListOptions) (page Page, err error) {
	if tenantID == "" || options.Limit < 1 {
		return Page{}, ErrInvalidPage
	}
	options.Search = strings.TrimSpace(options.Search)

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
WITH batches AS (
    SELECT COALESCE(v.batch_id, v.id) AS id,
           p.name AS plan_name,
           COUNT(*)::bigint AS issued,
           COUNT(*) FILTER (WHERE v.status = 'REDEEMED')::bigint AS redeemed,
           COUNT(*) FILTER (WHERE v.status = 'UNUSED')::bigint AS available,
           MIN(v.expires_at) AS expires_at,
           MIN(v.created_at) AS created_at,
           CASE
               WHEN bool_or(
                        v.status = 'UNUSED'
                        AND (v.expires_at IS NULL OR v.expires_at > now())
                    )
                   THEN 'ACTIVE'
               WHEN COUNT(*) FILTER (WHERE v.status = 'UNUSED') > 0
                   THEN 'EXPIRED'
               WHEN COUNT(*) FILTER (WHERE v.status = 'REDEEMED') = COUNT(*)
                   THEN 'COMPLETED'
               WHEN COUNT(*) FILTER (WHERE v.status = 'REVOKED') = COUNT(*)
                   THEN 'REVOKED'
               WHEN COUNT(*) FILTER (WHERE v.status = 'EXPIRED') = COUNT(*)
                   THEN 'EXPIRED'
               ELSE 'MIXED'
           END AS status
      FROM vouchers AS v
      JOIN plans AS p
        ON p.id = v.plan_id
       AND p.tenant_id = v.tenant_id
     WHERE v.tenant_id = $1
     GROUP BY COALESCE(v.batch_id, v.id), p.name
)
SELECT id::text,
       plan_name,
       issued,
       redeemed,
       available,
       status,
       expires_at,
       created_at
  FROM batches
 WHERE (
       $2 = ''
       OR plan_name ILIKE '%' || $2 || '%'
       OR id::text ILIKE '%' || $2 || '%'
   )
   AND (
       $3::timestamptz IS NULL
       OR (created_at, id) < ($3::timestamptz, $4::uuid)
   )
 ORDER BY created_at DESC, id DESC
 LIMIT $5`,
			tenantID,
			options.Search,
			nullableCursorTime(options.Cursor),
			nullableCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query voucher batches: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var batch Batch
			var expiresAt pgtype.Timestamptz
			if err := rows.Scan(
				&batch.ID,
				&batch.PlanName,
				&batch.Issued,
				&batch.Redeemed,
				&batch.Available,
				&batch.Status,
				&expiresAt,
				&batch.CreatedAt,
			); err != nil {
				return fmt.Errorf("scan voucher batch: %w", err)
			}
			if expiresAt.Valid {
				value := expiresAt.Time.UTC()
				batch.ExpiresAt = &value
			}
			page.Batches = append(page.Batches, batch)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate voucher batches: %w", err)
		}
		if len(page.Batches) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Batches = page.Batches[:options.Limit]
		last := page.Batches[len(page.Batches)-1]
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
