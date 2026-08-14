package sessions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads session state through transaction-scoped tenant RLS.
// Explicit tenant predicates remain on every relationship, including the
// latest accounting and quota read models.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("sessions: database pool is required")
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
SELECT s.id::text,
       c.id::text,
       c.customer_number,
       COALESCE(c.first_name, ''),
       COALESCE(c.last_name, ''),
       p.name,
       COALESCE(r.id::text, ''),
       COALESCE(r.name, 'Unassigned'),
       COALESCE(s.ip_address::text, ''),
       s.status,
       s.started_at,
       s.last_interim_at,
       COALESCE(latest.session_bytes::text, '0'),
       COALESCE(quota.consumed_bytes::text, ''),
       COALESCE(quota.quota_bytes::text, '')
  FROM sessions AS s
  JOIN customers AS c
    ON c.id = s.customer_id
   AND c.tenant_id = s.tenant_id
  JOIN subscriptions AS sub
    ON sub.id = s.subscription_id
   AND sub.tenant_id = s.tenant_id
  JOIN plans AS p
    ON p.id = sub.plan_id
   AND p.tenant_id = s.tenant_id
  LEFT JOIN routers AS r
    ON r.id = s.router_id
   AND r.tenant_id = s.tenant_id
  LEFT JOIN LATERAL (
      SELECT ar.input_octets::numeric
             + ar.input_gigawords::numeric * 4294967296
             + ar.output_octets::numeric
             + ar.output_gigawords::numeric * 4294967296 AS session_bytes
        FROM accounting_records AS ar
       WHERE ar.tenant_id = s.tenant_id
         AND ar.session_id = s.id
       ORDER BY ar.event_timestamp DESC, ar.created_at DESC
       LIMIT 1
  ) AS latest ON true
  LEFT JOIN LATERAL (
      SELECT uc.consumed_bytes, uc.quota_bytes
        FROM usage_counters AS uc
       WHERE uc.tenant_id = s.tenant_id
         AND uc.subscription_id = s.subscription_id
         AND uc.period_start <= now()
         AND uc.period_end > now()
       ORDER BY uc.period_start DESC
       LIMIT 1
  ) AS quota ON true
 WHERE s.tenant_id = $1
   AND (
       $2 = ''
       OR c.customer_number ILIKE '%' || $2 || '%'
       OR COALESCE(c.first_name, '') ILIKE '%' || $2 || '%'
       OR COALESCE(c.last_name, '') ILIKE '%' || $2 || '%'
       OR COALESCE(r.name, '') ILIKE '%' || $2 || '%'
       OR COALESCE(s.ip_address::text, '') ILIKE '%' || $2 || '%'
   )
   AND ($3 = '' OR s.status = $3)
   AND (
       $4::timestamptz IS NULL
       OR (s.started_at, s.id) < ($4::timestamptz, $5::uuid)
   )
 ORDER BY s.started_at DESC, s.id DESC
 LIMIT $6`,
			tenantID,
			options.Search,
			string(options.Status),
			nullableCursorTime(options.Cursor),
			nullableCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query sessions: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var session Session
			if err := rows.Scan(
				&session.ID,
				&session.CustomerID,
				&session.CustomerNumber,
				&session.CustomerFirstName,
				&session.CustomerLastName,
				&session.PlanName,
				&session.RouterID,
				&session.RouterName,
				&session.IPAddress,
				&session.Status,
				&session.StartedAt,
				&session.LastInterimAt,
				&session.SessionBytes,
				&session.UsageConsumedBytes,
				&session.UsageQuotaBytes,
			); err != nil {
				return fmt.Errorf("scan session: %w", err)
			}
			page.Sessions = append(page.Sessions, session)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate sessions: %w", err)
		}
		if len(page.Sessions) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Sessions = page.Sessions[:options.Limit]
		last := page.Sessions[len(page.Sessions)-1]
		page.Next = Cursor{StartedAt: last.StartedAt, ID: last.ID}
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
	return cursor.StartedAt
}

func nullableCursorID(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.ID
}
