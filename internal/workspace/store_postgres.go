package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads the tenant profile through a transaction that binds the
// tenant context. Counts use explicit tenant predicates as a second boundary.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("workspace: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) Get(ctx context.Context, tenantID string) (snapshot Snapshot, err error) {
	if tenantID == "" {
		return Snapshot{}, ErrUnavailable
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
SELECT tenant.name,
       tenant.slug,
       tenant.timezone,
       tenant.currency,
       tenant.status,
       tenant.updated_at,
       (
           SELECT COUNT(*)
             FROM routers AS router
            WHERE router.tenant_id = tenant.id
              AND router.status <> 'RETIRED'
       ) AS registered_routers,
       (
           SELECT COUNT(*)
             FROM users AS member
            WHERE member.tenant_id = tenant.id
              AND member.status = 'ACTIVE'
       ) AS active_team_members
  FROM tenants AS tenant
 WHERE tenant.id = $1`,
			tenantID,
		).Scan(
			&snapshot.Name,
			&snapshot.Slug,
			&snapshot.Timezone,
			&snapshot.Currency,
			&snapshot.Status,
			&snapshot.UpdatedAt,
			&snapshot.RegisteredRouters,
			&snapshot.ActiveTeamMembers,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("query workspace settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}
