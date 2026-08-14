package automations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads workflow inventory in the tenant RLS transaction
// boundary. Owner detail is reduced to an operator-safe identity label.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("automations: database pool is required")
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
SELECT workflow.id::text,
       workflow.name,
       workflow.trigger_description,
       workflow.status,
       workflow.next_run_at,
       COALESCE(NULLIF(owner.email::text, ''), NULLIF(owner.phone, ''), 'Unassigned') AS owner,
       workflow.updated_at
  FROM automation_workflows AS workflow
  LEFT JOIN users AS owner
    ON owner.id = workflow.owner_id
   AND owner.tenant_id = workflow.tenant_id
 WHERE workflow.tenant_id = $1
   AND (
       $2 = ''
       OR workflow.name ILIKE '%' || $2 || '%'
       OR workflow.trigger_description ILIKE '%' || $2 || '%'
       OR COALESCE(owner.email::text, '') ILIKE '%' || $2 || '%'
       OR COALESCE(owner.phone, '') ILIKE '%' || $2 || '%'
   )
   AND ($3 = '' OR workflow.status = $3)
   AND (
       $4::timestamptz IS NULL
       OR (workflow.updated_at, workflow.id) < ($4::timestamptz, $5::uuid)
   )
 ORDER BY workflow.updated_at DESC, workflow.id DESC
 LIMIT $6`,
			tenantID,
			options.Search,
			string(options.Status),
			nullableCursorTime(options.Cursor),
			nullableCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query automation workflows: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var workflow Workflow
			var nextRunAt pgtype.Timestamptz
			if err := rows.Scan(
				&workflow.ID,
				&workflow.Name,
				&workflow.TriggerDescription,
				&workflow.Status,
				&nextRunAt,
				&workflow.Owner,
				&workflow.UpdatedAt,
			); err != nil {
				return fmt.Errorf("scan automation workflow: %w", err)
			}
			if nextRunAt.Valid {
				value := nextRunAt.Time.UTC()
				workflow.NextRunAt = &value
			}
			page.Workflows = append(page.Workflows, workflow)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate automation workflows: %w", err)
		}
		if len(page.Workflows) <= options.Limit {
			return nil
		}
		page.HasMore = true
		page.Workflows = page.Workflows[:options.Limit]
		last := page.Workflows[len(page.Workflows)-1]
		page.Next = Cursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
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
	return cursor.UpdatedAt
}

func nullableCursorID(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.ID
}
