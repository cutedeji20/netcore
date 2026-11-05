package vouchers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	return s.listBatches(ctx, tenantID, "", options)
}

func (s *PostgresStore) ListBuyerBatches(ctx context.Context, tenantID, userID string, options ListOptions) (Page, error) {
	if !validUUID(userID) {
		return Page{}, ErrInvalidPage
	}
	return s.listBatches(ctx, tenantID, userID, options)
}

func (s *PostgresStore) Summary(ctx context.Context, tenantID string, startsAt, endsAt time.Time, period string) (summary Summary, err error) {
	if tenantID == "" || startsAt.IsZero() || endsAt.IsZero() || !endsAt.After(startsAt) {
		return Summary{}, ErrInvalidPage
	}
	summary = Summary{Period: period, StartsAt: startsAt.UTC(), EndsAt: endsAt.UTC()}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
SELECT
  COUNT(*) FILTER (WHERE v.created_at >= $2 AND v.created_at < $3)::bigint,
  COUNT(*) FILTER (WHERE v.redeemed_at >= $2 AND v.redeemed_at < $3)::bigint,
  COUNT(*) FILTER (WHERE v.created_at >= $2 AND v.created_at < $3 AND v.status='REDEEMED')::bigint,
  COUNT(*) FILTER (WHERE v.status='UNUSED' AND v.expires_at >= $2 AND v.expires_at < $3 AND v.expires_at <= now())::bigint,
  COUNT(*) FILTER (WHERE v.status='UNUSED' AND (v.expires_at IS NULL OR v.expires_at > now()))::bigint,
  COUNT(*) FILTER (WHERE v.status='REVOKED')::bigint
FROM vouchers v
WHERE v.tenant_id=$1::uuid`, tenantID, summary.StartsAt, summary.EndsAt).Scan(
			&summary.Issued,
			&summary.Redeemed,
			&summary.RedeemedFromIssued,
			&summary.ExpiredUnused,
			&summary.Available,
			&summary.Revoked,
		); err != nil {
			return fmt.Errorf("query voucher summary: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT COUNT(*)::bigint FROM subscriptions WHERE tenant_id=$1::uuid AND voucher_id IS NOT NULL AND status='PENDING' AND expires_at>now()`, tenantID).Scan(&summary.QueuedRedemptions); err != nil {
			return fmt.Errorf("query queued voucher redemptions: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE buyer_user_id IS NOT NULL)::bigint,COUNT(*) FILTER (WHERE issuer_user_id IS NOT NULL)::bigint FROM voucher_batches WHERE tenant_id=$1::uuid AND created_at >= $2 AND created_at < $3`, tenantID, summary.StartsAt, summary.EndsAt).Scan(&summary.CustomerBatches, &summary.StaffBatches); err != nil {
			return fmt.Errorf("query voucher batch ownership: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT COUNT(*)::bigint,COALESCE(SUM(amount_minor),0)::bigint,COALESCE(SUM(unit_discount_minor*quantity),0)::bigint,COALESCE(MAX(currency),'') FROM voucher_orders WHERE tenant_id=$1::uuid AND status='PAID' AND paid_at >= $2 AND paid_at < $3`, tenantID, summary.StartsAt, summary.EndsAt).Scan(&summary.PaidOrders, &summary.CollectedMinor, &summary.DiscountMinor, &summary.Currency); err != nil {
			return fmt.Errorf("query voucher order summary: %w", err)
		}
		return nil
	})
	if err != nil {
		return Summary{}, err
	}
	return summary, nil
}

func (s *PostgresStore) listBatches(ctx context.Context, tenantID, buyerUserID string, options ListOptions) (page Page, err error) {
	if tenantID == "" || options.Limit < 1 {
		return Page{}, ErrInvalidPage
	}
	options.Search = strings.TrimSpace(options.Search)
	options.Status = strings.ToUpper(strings.TrimSpace(options.Status))
	if _, ok := voucherBatchStatuses[options.Status]; !ok {
		return Page{}, ErrInvalidPage
	}

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
WITH batches AS (
    SELECT COALESCE(v.batch_id, v.id) AS id,
           p.name AS plan_name,
		   COALESCE(NULLIF(btrim(concat_ws(' ', buyer_customer.first_name, buyer_customer.last_name)), ''), buyer.email::text, issuer.email::text, 'Manual or legacy batch') AS owner_display,
		   concat_ws(' ', buyer_customer.first_name, buyer_customer.last_name, buyer_customer.customer_number, buyer_customer.email::text, buyer_customer.phone, buyer.email::text, issuer.email::text) AS owner_search,
		   CASE WHEN b.buyer_user_id IS NOT NULL THEN 'CUSTOMER' WHEN b.issuer_user_id IS NOT NULL THEN 'STAFF' ELSE 'UNKNOWN' END AS owner_type,
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
      LEFT JOIN voucher_batches AS b ON b.id=v.batch_id AND b.tenant_id=v.tenant_id
      LEFT JOIN users AS buyer ON buyer.id=b.buyer_user_id AND buyer.tenant_id=v.tenant_id
      LEFT JOIN customers AS buyer_customer ON buyer_customer.user_id=buyer.id AND buyer_customer.tenant_id=v.tenant_id
      LEFT JOIN users AS issuer ON issuer.id=b.issuer_user_id AND issuer.tenant_id=v.tenant_id
     WHERE v.tenant_id = $1 AND ($6::uuid IS NULL OR b.buyer_user_id=$6::uuid)
     GROUP BY COALESCE(v.batch_id, v.id), p.name, b.buyer_user_id, b.issuer_user_id,
              buyer_customer.first_name, buyer_customer.last_name, buyer_customer.customer_number, buyer_customer.email, buyer_customer.phone, buyer.email, issuer.email
)
SELECT id::text,
       plan_name,
	   owner_display,
	   owner_type,
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
	   OR owner_search ILIKE '%' || $2 || '%'
   )
	   AND ($7 = '' OR status = $7)
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
			nullableVoucherUUID(buyerUserID),
			options.Status,
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
				&batch.OwnerDisplay,
				&batch.OwnerType,
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
