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
	       AND p.cleared_at IS NULL

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

func (s *PostgresStore) Clear(ctx context.Context, tenantID string, actor MutationActor, request ClearRequest) (count int, err error) {
	if !validUUID(tenantID) || !validUUID(actor.UserID) || !validClearRequest(request) {
		return 0, ErrInvalidClear
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		where, args := clearWhere(request)
		if request.Scope == ClearSelected {
			var eligible int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM payments WHERE tenant_id = $1 AND cleared_at IS NULL AND status IN ('PENDING', 'FAILED', 'ABANDONED') AND id = ANY($2::uuid[])`, tenantID, request.PaymentIDs).Scan(&eligible); err != nil {
				return fmt.Errorf("check selected payment attempts: %w", err)
			}
			if eligible != len(request.PaymentIDs) {
				return ErrInvalidClear
			}
		}

		rows, err := tx.Query(ctx, `UPDATE payments
SET status = 'ABANDONED', cleared_at = now(), cleared_by = $2::uuid, updated_at = now()
WHERE tenant_id = $1 AND cleared_at IS NULL AND `+where+`
RETURNING id::text, COALESCE(subscription_id::text, ''), provider_reference, status`, append([]any{tenantID, actor.UserID}, args...)...)
		if err != nil {
			return fmt.Errorf("clear payment attempts: %w", err)
		}
		defer rows.Close()
		type clearedPayment struct{ id, subscriptionID, reference, status string }
		var cleared []clearedPayment
		for rows.Next() {
			var item clearedPayment
			if err := rows.Scan(&item.id, &item.subscriptionID, &item.reference, &item.status); err != nil {
				return fmt.Errorf("scan cleared payment attempt: %w", err)
			}
			cleared = append(cleared, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate cleared payment attempts: %w", err)
		}
		if len(cleared) == 0 {
			return nil
		}

		references := make([]string, 0, len(cleared))
		subscriptions := make([]string, 0, len(cleared))
		for _, item := range cleared {
			references = append(references, item.reference)
			if item.subscriptionID != "" {
				subscriptions = append(subscriptions, item.subscriptionID)
			}
		}
		if len(subscriptions) > 0 {
			cancelledRows, err := tx.Query(ctx, `UPDATE subscriptions SET status = 'CANCELLED', updated_at = now() WHERE tenant_id = $1 AND id = ANY($2::uuid[]) AND status = 'PENDING' RETURNING id::text`, tenantID, subscriptions)
			if err != nil {
				return fmt.Errorf("cancel pending subscriptions: %w", err)
			}
			var cancelled []string
			for cancelledRows.Next() {
				var subscriptionID string
				if err := cancelledRows.Scan(&subscriptionID); err != nil {
					cancelledRows.Close()
					return fmt.Errorf("scan cancelled subscription: %w", err)
				}
				cancelled = append(cancelled, subscriptionID)
			}
			if err := cancelledRows.Err(); err != nil {
				cancelledRows.Close()
				return fmt.Errorf("iterate cancelled subscriptions: %w", err)
			}
			cancelledRows.Close()
			for _, subscriptionID := range cancelled {
				if _, err := tx.Exec(ctx, `INSERT INTO subscription_events (tenant_id, subscription_id, from_status, to_status, reason, actor_type, actor_id, metadata)
VALUES ($1, $2::uuid, 'PENDING', 'CANCELLED', 'PAYMENT_ATTEMPT_CLEARED', 'ADMIN', $3::uuid, '{}'::jsonb)`, tenantID, subscriptionID, actor.UserID); err != nil {
					return fmt.Errorf("write cancellation event: %w", err)
				}
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM idempotency_keys WHERE tenant_id = $1 AND endpoint = 'POST /api/v1/payments' AND response_body->>'provider_reference' = ANY($2::text[])`, tenantID, references); err != nil {
			return fmt.Errorf("clear payment idempotency records: %w", err)
		}
		for _, item := range cleared {
			if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, metadata, ip_address, user_agent)
VALUES ($1, 'USER', $2::uuid, 'PAYMENT_ATTEMPT_CLEARED', 'payment', $3::uuid, jsonb_build_object('scope', $4::text), NULLIF($5, '')::inet, NULLIF($6, ''))`, tenantID, actor.UserID, item.id, string(request.Scope), actor.IP, actor.UserAgent); err != nil {
				return fmt.Errorf("write payment clear audit record: %w", err)
			}
		}
		count = len(cleared)
		return nil
	})
	return count, err
}

func clearWhere(request ClearRequest) (string, []any) {
	switch request.Scope {
	case ClearPending:
		return "status = 'PENDING'", nil
	case ClearFailed:
		return "status = 'FAILED'", nil
	case ClearSelected:
		return "status IN ('PENDING', 'FAILED', 'ABANDONED') AND id = ANY($3::uuid[])", []any{request.PaymentIDs}
	default:
		return "FALSE", nil
	}
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
