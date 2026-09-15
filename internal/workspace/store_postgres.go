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
	       tenant.require_email_verification,
	       tenant.require_phone_verification,
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
			&snapshot.RequireEmailVerification,
			&snapshot.RequirePhoneVerification,
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

func (s *PostgresStore) SetVerificationPolicy(ctx context.Context, tenantID, actorID string, emailRequired, phoneRequired bool) (Snapshot, error) {
	if tenantID == "" || actorID == "" {
		return Snapshot{}, ErrUnavailable
	}
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
UPDATE tenants
   SET require_email_verification = $2,
       require_phone_verification = $3,
       updated_at = now()
 WHERE id = $1`, tenantID, emailRequired, phoneRequired); err != nil {
			return fmt.Errorf("update verification policy: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, metadata)
VALUES ($1, 'USER', $2::uuid, 'REGISTRATION_VERIFICATION_POLICY_UPDATED', 'tenant', $1::uuid,
        jsonb_build_object('email_required', $3::boolean, 'phone_required', $4::boolean))`, tenantID, actorID, emailRequired, phoneRequired); err != nil {
			return fmt.Errorf("write verification policy audit record: %w", err)
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx, tenantID)
}
