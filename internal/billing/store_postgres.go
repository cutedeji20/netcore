package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads invoices and payments through the tenant RLS
// transaction boundary. Customer tenant predicates are explicit in both arms
// of the unified query.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("billing: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, options ListOptions) (page Page, err error) {
	if tenantID == "" || options.Limit < 1 || (options.Source != "" && !IsValidSource(options.Source)) {
		return Page{}, ErrInvalidPage
	}
	options.Search = strings.TrimSpace(options.Search)

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
WITH transactions AS (
    SELECT 'PAYMENT'::text AS source,
           p.id,
           p.provider_reference AS reference,
           c.id AS customer_id,
           c.customer_number,
           COALESCE(c.first_name, '') AS first_name,
           COALESCE(c.last_name, '') AS last_name,
           p.amount_minor,
           p.currency,
           p.status,
           COALESCE(p.verified_at, p.created_at) AS recorded_at,
           NULL::timestamptz AS due_at,
           p.verified_at
      FROM payments AS p
      JOIN customers AS c
        ON c.id = p.customer_id
       AND c.tenant_id = p.tenant_id
     WHERE p.tenant_id = $1

    UNION ALL

    SELECT 'INVOICE'::text AS source,
           i.id,
           i.invoice_number AS reference,
           c.id AS customer_id,
           c.customer_number,
           COALESCE(c.first_name, '') AS first_name,
           COALESCE(c.last_name, '') AS last_name,
           i.amount_minor,
           i.currency,
           i.status,
           COALESCE(i.paid_at, i.issued_at, i.created_at) AS recorded_at,
           i.due_at,
           NULL::timestamptz AS verified_at
      FROM invoices AS i
      JOIN customers AS c
        ON c.id = i.customer_id
       AND c.tenant_id = i.tenant_id
     WHERE i.tenant_id = $1
)
SELECT source,
       id::text,
       reference,
       customer_id::text,
       customer_number,
       first_name,
       last_name,
       amount_minor,
       currency,
       status,
       recorded_at,
       due_at,
       verified_at
  FROM transactions
 WHERE ($2 = '' OR source = $2)
   AND (
       $3 = ''
       OR reference ILIKE '%' || $3 || '%'
       OR customer_number ILIKE '%' || $3 || '%'
       OR first_name ILIKE '%' || $3 || '%'
       OR last_name ILIKE '%' || $3 || '%'
   )
   AND (
       $4::timestamptz IS NULL
       OR (recorded_at, source, id) < ($4::timestamptz, $5::text, $6::uuid)
   )
 ORDER BY recorded_at DESC, source DESC, id DESC
 LIMIT $7`,
			tenantID,
			string(options.Source),
			options.Search,
			nullableCursorTime(options.Cursor),
			nullableCursorSource(options.Cursor),
			nullableCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query billing transactions: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var transaction Transaction
			var dueAt, verifiedAt pgtype.Timestamptz
			if err := rows.Scan(
				&transaction.Source,
				&transaction.ID,
				&transaction.Reference,
				&transaction.CustomerID,
				&transaction.CustomerNumber,
				&transaction.CustomerFirstName,
				&transaction.CustomerLastName,
				&transaction.AmountMinor,
				&transaction.Currency,
				&transaction.Status,
				&transaction.RecordedAt,
				&dueAt,
				&verifiedAt,
			); err != nil {
				return fmt.Errorf("scan billing transaction: %w", err)
			}
			if dueAt.Valid {
				value := dueAt.Time.UTC()
				transaction.DueAt = &value
			}
			if verifiedAt.Valid {
				value := verifiedAt.Time.UTC()
				transaction.VerifiedAt = &value
			}
			page.Transactions = append(page.Transactions, transaction)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate billing transactions: %w", err)
		}
		if len(page.Transactions) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Transactions = page.Transactions[:options.Limit]
		last := page.Transactions[len(page.Transactions)-1]
		page.Next = Cursor{RecordedAt: last.RecordedAt, Source: last.Source, ID: last.ID}
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
	return cursor.RecordedAt
}

func nullableCursorSource(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return string(cursor.Source)
}

func nullableCursorID(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.ID
}
