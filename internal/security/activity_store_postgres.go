package security

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/netcore-isp/netcore/internal/database"
)

// ActivityPostgresStore reads the immutable tenant audit trail inside the RLS
// transaction boundary. It projects only operator-safe text, never telemetry
// or the raw JSON metadata recorded for forensic purposes.
type ActivityPostgresStore struct{ db *database.Pool }

func NewActivityPostgresStore(db *database.Pool) (*ActivityPostgresStore, error) {
	if db == nil {
		return nil, errors.New("security: database pool is required")
	}
	return &ActivityPostgresStore{db: db}, nil
}

func (s *ActivityPostgresStore) ListActivity(ctx context.Context, tenantID string, options ActivityListOptions) (page ActivityPage, err error) {
	if tenantID == "" || options.Limit < 1 {
		return ActivityPage{}, ErrInvalidActivityPage
	}
	options.Search = strings.TrimSpace(options.Search)

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT audit.id::text,
       audit.action,
       CASE
           WHEN UPPER(audit.actor_type) = 'SYSTEM' THEN 'System'
           WHEN COALESCE(NULLIF(actor.email::text, ''), NULLIF(actor.phone, '')) IS NOT NULL
               THEN COALESCE(NULLIF(actor.email::text, ''), NULLIF(actor.phone, ''))
           ELSE 'Team member'
       END AS actor,
       COALESCE(NULLIF(audit.resource_type, ''), 'Account') AS resource_type,
       audit.created_at
  FROM audit_logs AS audit
  LEFT JOIN users AS actor
    ON actor.id = audit.actor_id
   AND actor.tenant_id = audit.tenant_id
 WHERE audit.tenant_id = $1
   AND (
       $2 = ''
       OR audit.action ILIKE '%' || $2 || '%'
       OR COALESCE(audit.resource_type, '') ILIKE '%' || $2 || '%'
       OR COALESCE(actor.email::text, '') ILIKE '%' || $2 || '%'
       OR COALESCE(actor.phone, '') ILIKE '%' || $2 || '%'
   )
   AND (
       $3::timestamptz IS NULL
       OR (audit.created_at, audit.id) < ($3::timestamptz, $4::uuid)
   )
 ORDER BY audit.created_at DESC, audit.id DESC
 LIMIT $5`,
			tenantID,
			options.Search,
			nullableActivityCursorTime(options.Cursor),
			nullableActivityCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query security activity: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var event ActivityEvent
			if err := rows.Scan(
				&event.ID,
				&event.Action,
				&event.Actor,
				&event.ResourceType,
				&event.CreatedAt,
			); err != nil {
				return fmt.Errorf("scan security activity: %w", err)
			}
			page.Events = append(page.Events, event)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate security activity: %w", err)
		}
		if len(page.Events) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Events = page.Events[:options.Limit]
		last := page.Events[len(page.Events)-1]
		page.Next = ActivityCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		return nil
	})
	if err != nil {
		return ActivityPage{}, err
	}
	return page, nil
}

func nullableActivityCursorTime(cursor ActivityCursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.CreatedAt
}

func nullableActivityCursorID(cursor ActivityCursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.ID
}
