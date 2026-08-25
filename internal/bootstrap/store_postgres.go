package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/netcore-isp/netcore/internal/database"
	"github.com/netcore-isp/netcore/internal/team"
)

// PostgresStore uses the protected migration owner only for this local,
// one-time ceremony. The normal API login is intentionally unable to create
// tenants or assign every permission.
type PostgresStore struct {
	db *database.Pool
}

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("bootstrap: database is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) BootstrapFirstAdmin(ctx context.Context, record Record) (result Result, err error) {
	if s == nil || s.db == nil {
		return Result{}, errors.New("bootstrap: database is required")
	}
	err = s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		// Serialise the ceremony independently of table contents. A fresh
		// database has no row to lock, so row-level locks alone are insufficient.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('netcore:first-admin-bootstrap'))"); err != nil {
			return fmt.Errorf("bootstrap: lock first administrator ceremony: %w", err)
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM tenants) OR EXISTS (SELECT 1 FROM users)").Scan(&exists); err != nil {
			return fmt.Errorf("bootstrap: check existing operators: %w", err)
		}
		if exists {
			return ErrAlreadyBootstrapped
		}

		if err := tx.QueryRow(ctx, `
INSERT INTO tenants (name, slug, timezone, currency)
VALUES ($1, $2, $3, $4)
RETURNING id::text`, record.TenantName, record.TenantSlug, record.Timezone, record.Currency).Scan(&result.TenantID); err != nil {
			return fmt.Errorf("bootstrap: create tenant: %w", err)
		}
		if err := tx.QueryRow(ctx, `
INSERT INTO users (tenant_id, email, password_hash, status, email_verified_at)
VALUES ($1, $2, $3, 'ACTIVE', now())
RETURNING id::text`, result.TenantID, record.Email, record.PasswordHash).Scan(&result.UserID); err != nil {
			return fmt.Errorf("bootstrap: create first administrator: %w", err)
		}

		roleIDs := make(map[team.BuiltInRole]string, len(record.InitialRoles))
		for _, role := range record.InitialRoles {
			var roleID string
			if err := tx.QueryRow(ctx, `
INSERT INTO roles (tenant_id, name)
VALUES ($1, $2)
RETURNING id::text`, result.TenantID, string(role)).Scan(&roleID); err != nil {
				return fmt.Errorf("bootstrap: create %s role: %w", role, err)
			}
			roleIDs[role] = roleID

			var tag pgconn.CommandTag
			var err error
			if role == team.RoleAdministrator {
				tag, err = tx.Exec(ctx, `
INSERT INTO role_permissions (role_id, permission_id)
SELECT $1, id FROM permissions`, roleID)
			} else {
				tag, err = tx.Exec(ctx, `
INSERT INTO role_permissions (role_id, permission_id)
SELECT $1, id FROM permissions WHERE name = ANY($2)`, roleID, role.Permissions())
			}
			if err != nil {
				return fmt.Errorf("bootstrap: assign %s permissions: %w", role, err)
			}
			if tag.RowsAffected() == 0 {
				return errors.New("bootstrap: permission catalogue is empty; run migrations first")
			}
		}
		for _, role := range record.InitialRoleAssignments {
			roleID, ok := roleIDs[role]
			if !ok {
				return fmt.Errorf("bootstrap: initial role assignment %q was not seeded", role)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)", result.UserID, roleID); err != nil {
				return fmt.Errorf("bootstrap: assign %s role: %w", role, err)
			}
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO user_mfa_totp (tenant_id, user_id, secret_ref, status, last_used_counter, enabled_at)
VALUES ($1, $2, $3, 'ACTIVE', $4, now())`, result.TenantID, result.UserID, record.TOTPSecretRef, record.InitialTOTPCounter); err != nil {
			return fmt.Errorf("bootstrap: activate administrator MFA device: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, metadata)
VALUES ($1, 'SYSTEM', $2, 'ADMIN_BOOTSTRAPPED', 'users', $2, jsonb_build_object('source', 'local-one-time-bootstrap'))`, result.TenantID, result.UserID); err != nil {
			return fmt.Errorf("bootstrap: write audit record: %w", err)
		}
		return nil
	})
	return result, err
}
