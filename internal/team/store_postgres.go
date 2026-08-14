package team

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads users, tenant roles, and MFA posture within the
// transaction-scoped RLS boundary. Session activity is reduced to its latest
// timestamp; session credentials and network metadata never leave PostgreSQL.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("team: database pool is required")
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
SELECT u.id::text,
       COALESCE(u.email::text, ''),
       COALESCE(u.phone, ''),
       u.status,
       COALESCE(
           array_agg(DISTINCT r.name ORDER BY r.name) FILTER (WHERE r.id IS NOT NULL),
           ARRAY[]::text[]
       ) AS roles,
       mfa.status,
       activity.last_seen_at,
       u.created_at
  FROM users AS u
  LEFT JOIN user_roles AS ur
    ON ur.user_id = u.id
  LEFT JOIN roles AS r
    ON r.id = ur.role_id
   AND r.tenant_id = u.tenant_id
  CROSS JOIN LATERAL (
      SELECT CASE
          WHEN bool_or(device.status = 'ACTIVE') THEN 'ENABLED'
          WHEN bool_or(device.status = 'PENDING') THEN 'PENDING'
          ELSE 'NOT_ENABLED'
      END AS status
        FROM user_mfa_totp AS device
       WHERE device.tenant_id = u.tenant_id
         AND device.user_id = u.id
  ) AS mfa
  LEFT JOIN LATERAL (
      SELECT session.last_seen_at
        FROM auth_sessions AS session
       WHERE session.tenant_id = u.tenant_id
         AND session.user_id = u.id
       ORDER BY session.last_seen_at DESC
       LIMIT 1
  ) AS activity ON true
 WHERE u.tenant_id = $1
   AND (
       $2 = ''
       OR COALESCE(u.email::text, '') ILIKE '%' || $2 || '%'
       OR COALESCE(u.phone, '') ILIKE '%' || $2 || '%'
       OR EXISTS (
           SELECT 1
             FROM user_roles AS search_ur
             JOIN roles AS search_role
               ON search_role.id = search_ur.role_id
              AND search_role.tenant_id = u.tenant_id
            WHERE search_ur.user_id = u.id
              AND search_role.name ILIKE '%' || $2 || '%'
       )
   )
   AND (
       $3::timestamptz IS NULL
       OR (u.created_at, u.id) < ($3::timestamptz, $4::uuid)
   )
 GROUP BY u.id, mfa.status, activity.last_seen_at
 ORDER BY u.created_at DESC, u.id DESC
 LIMIT $5`,
			tenantID,
			options.Search,
			nullableCursorTime(options.Cursor),
			nullableCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query team members: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var member Member
			var lastSeenAt pgtype.Timestamptz
			if err := rows.Scan(
				&member.ID,
				&member.Email,
				&member.Phone,
				&member.Status,
				&member.Roles,
				&member.MFAStatus,
				&lastSeenAt,
				&member.CreatedAt,
			); err != nil {
				return fmt.Errorf("scan team member: %w", err)
			}
			if lastSeenAt.Valid {
				value := lastSeenAt.Time.UTC()
				member.LastSeenAt = &value
			}
			page.Members = append(page.Members, member)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate team members: %w", err)
		}
		if len(page.Members) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Members = page.Members[:options.Limit]
		last := page.Members[len(page.Members)-1]
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
