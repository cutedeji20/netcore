package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads customer rows through the transaction-scoped tenant RLS
// boundary. The explicit tenant predicate remains present in every query as a
// second guard against accidental cross-tenant access.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("customers: database pool is required")
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
SELECT id::text,
       customer_number,
       status,
       COALESCE(first_name, ''),
       COALESCE(last_name, ''),
       COALESCE(phone, ''),
       COALESCE(email::text, ''),
       created_at,
       updated_at
  FROM customers
 WHERE tenant_id = $1
   AND (
       $2 = ''
       OR customer_number ILIKE '%' || $2 || '%'
       OR COALESCE(first_name, '') ILIKE '%' || $2 || '%'
       OR COALESCE(last_name, '') ILIKE '%' || $2 || '%'
       OR COALESCE(phone, '') ILIKE '%' || $2 || '%'
       OR COALESCE(email::text, '') ILIKE '%' || $2 || '%'
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
			return fmt.Errorf("query customers: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var customer Customer
			if err := rows.Scan(
				&customer.ID,
				&customer.CustomerNumber,
				&customer.Status,
				&customer.FirstName,
				&customer.LastName,
				&customer.Phone,
				&customer.Email,
				&customer.CreatedAt,
				&customer.UpdatedAt,
			); err != nil {
				return fmt.Errorf("scan customer: %w", err)
			}
			page.Customers = append(page.Customers, customer)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate customers: %w", err)
		}
		if len(page.Customers) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Customers = page.Customers[:options.Limit]
		last := page.Customers[len(page.Customers)-1]
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
