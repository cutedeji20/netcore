package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads subscriptions through transaction-scoped tenant RLS.
// Each joined tenant_id is checked explicitly too, so a malformed historical
// foreign-key relationship cannot leak another tenant's customer or plan.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("subscriptions: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, options ListOptions) (page Page, err error) {
	if tenantID == "" || options.Limit < 1 || (options.Status != "" && !IsValid(options.Status)) {
		return Page{}, ErrInvalidPage
	}
	options.Search = strings.TrimSpace(options.Search)

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT s.id::text,
       s.customer_id::text,
       c.customer_number,
       COALESCE(c.first_name, ''),
       COALESCE(c.last_name, ''),
       s.plan_id::text,
       p.name,
       s.status,
       s.starts_at,
       s.expires_at,
       s.auto_renew,
       s.payment_status,
       s.created_at,
       s.updated_at
  FROM subscriptions AS s
  JOIN customers AS c
    ON c.id = s.customer_id
   AND c.tenant_id = s.tenant_id
  JOIN plans AS p
    ON p.id = s.plan_id
   AND p.tenant_id = s.tenant_id
 WHERE s.tenant_id = $1
   AND (
       $2 = ''
       OR c.customer_number ILIKE '%' || $2 || '%'
       OR COALESCE(c.first_name, '') ILIKE '%' || $2 || '%'
       OR COALESCE(c.last_name, '') ILIKE '%' || $2 || '%'
       OR p.name ILIKE '%' || $2 || '%'
   )
   AND ($3 = '' OR s.status = $3)
   AND (
       $4::timestamptz IS NULL
       OR (s.created_at, s.id) < ($4::timestamptz, $5::uuid)
   )
 ORDER BY s.created_at DESC, s.id DESC
 LIMIT $6`,
			tenantID,
			options.Search,
			string(options.Status),
			nullableCursorTime(options.Cursor),
			nullableCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query subscriptions: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var subscription Subscription
			var startsAt, expiresAt pgtype.Timestamptz
			if err := rows.Scan(
				&subscription.ID,
				&subscription.CustomerID,
				&subscription.CustomerNumber,
				&subscription.CustomerFirstName,
				&subscription.CustomerLastName,
				&subscription.PlanID,
				&subscription.PlanName,
				&subscription.Status,
				&startsAt,
				&expiresAt,
				&subscription.AutoRenew,
				&subscription.PaymentStatus,
				&subscription.CreatedAt,
				&subscription.UpdatedAt,
			); err != nil {
				return fmt.Errorf("scan subscription: %w", err)
			}
			if startsAt.Valid {
				value := startsAt.Time.UTC()
				subscription.StartsAt = &value
			}
			if expiresAt.Valid {
				value := expiresAt.Time.UTC()
				subscription.ExpiresAt = &value
			}
			page.Subscriptions = append(page.Subscriptions, subscription)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate subscriptions: %w", err)
		}
		if len(page.Subscriptions) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Subscriptions = page.Subscriptions[:options.Limit]
		last := page.Subscriptions[len(page.Subscriptions)-1]
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
