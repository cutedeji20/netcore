package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads routers in a transaction carrying the tenant RLS
// setting. Tenant predicates on sites and NAS rows prevent a historical
// cross-tenant relationship from leaking network inventory.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("network: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, options ListOptions) (page Page, err error) {
	if tenantID == "" || options.Limit < 1 || (options.Status != "" && !IsValidRouterStatus(options.Status)) {
		return Page{}, ErrInvalidPage
	}
	options.Search = strings.TrimSpace(options.Search)

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT r.id::text,
       r.name,
       COALESCE(s.name, 'Unassigned'),
       r.status,
       aaa.status,
       r.last_seen_at
  FROM routers AS r
  LEFT JOIN sites AS s
    ON s.id = r.site_id
   AND s.tenant_id = r.tenant_id
  CROSS JOIN LATERAL (
      SELECT CASE
          WHEN bool_or(n.status = 'ACTIVE') THEN 'ACTIVE'
          WHEN COUNT(*) > 0 THEN 'DISABLED'
          ELSE 'NOT_CONFIGURED'
      END AS status
        FROM nas AS n
       WHERE n.tenant_id = r.tenant_id
         AND n.router_id = r.id
  ) AS aaa
 WHERE r.tenant_id = $1
   AND (
       $2 = ''
       OR r.name ILIKE '%' || $2 || '%'
       OR COALESCE(s.name, '') ILIKE '%' || $2 || '%'
   )
   AND ($3 = '' OR r.status = $3)
   AND ($4::text IS NULL OR r.name > $4::text)
 ORDER BY r.name ASC
 LIMIT $5`,
			tenantID,
			options.Search,
			string(options.Status),
			nullableCursorName(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query routers: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var router Router
			var lastSeenAt pgtype.Timestamptz
			if err := rows.Scan(
				&router.ID,
				&router.Name,
				&router.SiteName,
				&router.Status,
				&router.AAAStatus,
				&lastSeenAt,
			); err != nil {
				return fmt.Errorf("scan router: %w", err)
			}
			if lastSeenAt.Valid {
				value := lastSeenAt.Time.UTC()
				router.LastSeenAt = &value
			}
			page.Routers = append(page.Routers, router)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate routers: %w", err)
		}
		if len(page.Routers) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Routers = page.Routers[:options.Limit]
		page.Next = Cursor{Name: page.Routers[len(page.Routers)-1].Name}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func nullableCursorName(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.Name
}
