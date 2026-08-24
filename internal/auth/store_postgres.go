package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore persists identity state through the tenant-scoped database
// boundary. Every tenant-owned query includes both a tenant predicate and the
// transaction-local RLS setting: neither protection is relied upon alone.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("auth: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) ResolveTenant(ctx context.Context, slug string) (string, bool, error) {
	tenant, ok, err := s.db.ResolveActiveTenant(ctx, slug)
	if err != nil {
		return "", false, err
	}
	return tenant.ID, ok, nil
}

func (s *PostgresStore) FindUser(ctx context.Context, tenantID, identifier string) (user User, found bool, err error) {
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
SELECT u.id::text,
       u.tenant_id::text,
       COALESCE(u.email::text, ''),
       u.password_hash,
       u.status,
       u.email_verified_at IS NOT NULL,
       EXISTS (
           SELECT 1
             FROM user_roles AS ur
             JOIN roles AS r
               ON r.id = ur.role_id
              AND r.tenant_id = u.tenant_id
             JOIN role_permissions AS rp
               ON rp.role_id = r.id
             JOIN permissions AS p
               ON p.id = rp.permission_id
            WHERE ur.user_id = u.id
              AND p.name = 'auth.mfa_required'
       )
  FROM users AS u
 WHERE u.tenant_id = $1
   AND (u.email = $2 OR u.phone = $2)
 LIMIT 1`, tenantID, identifier).Scan(
			&user.ID, &user.TenantID, &user.Email, &user.PasswordHash, &user.Status, &user.EmailVerified, &user.RequiresMFA,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("query user: %w", err)
		}
		found = true
		return nil
	})
	return user, found, err
}

func (s *PostgresStore) CreateSession(ctx context.Context, user User, tokenHash []byte, expiresAt time.Time, ip, userAgent string) error {
	return s.db.InTenantTx(ctx, user.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO auth_sessions (tenant_id, user_id, token_hash, expires_at, ip_address, user_agent)
VALUES ($1, $2, $3, $4, NULLIF($5, '')::inet, NULLIF($6, ''))`,
			user.TenantID, user.ID, tokenHash, expiresAt, ip, userAgent,
		); err != nil {
			return fmt.Errorf("insert auth session: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, ip_address, user_agent)
VALUES ($1, 'USER', $2, 'AUTH_LOGIN', 'user', $2, NULLIF($3, '')::inet, NULLIF($4, ''))`,
			user.TenantID, user.ID, ip, userAgent,
		); err != nil {
			return fmt.Errorf("write login audit record: %w", err)
		}
		return nil
	})
}

func (s *PostgresStore) SessionPrincipal(ctx context.Context, tenantID string, tokenHash []byte) (principal Principal, found bool, err error) {
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
SELECT s.id::text,
       s.created_at,
       s.expires_at,
       s.tenant_id::text,
       u.id::text,
       COALESCE(u.email::text, '')
  FROM auth_sessions s
  JOIN users u ON u.id = s.user_id AND u.tenant_id = s.tenant_id
 WHERE s.tenant_id = $1
   AND s.token_hash = $2
   AND s.invalidated_at IS NULL
   AND s.expires_at > now()
   AND u.status = 'ACTIVE'`, tenantID, tokenHash).Scan(
			&principal.SessionID,
			&principal.SessionCreatedAt,
			&principal.SessionExpiresAt,
			&principal.TenantID,
			&principal.UserID,
			&principal.Email,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("query auth session: %w", err)
		}
		found = true

		rows, err := tx.Query(ctx, `
SELECT DISTINCT p.name
  FROM user_roles ur
  JOIN roles r ON r.id = ur.role_id AND r.tenant_id = $1
  JOIN role_permissions rp ON rp.role_id = r.id
  JOIN permissions p ON p.id = rp.permission_id
 WHERE ur.user_id = $2`, tenantID, principal.UserID)
		if err != nil {
			return fmt.Errorf("query principal permissions: %w", err)
		}
		defer rows.Close()
		principal.Permissions = make(map[string]struct{})
		for rows.Next() {
			var permission string
			if err := rows.Scan(&permission); err != nil {
				return fmt.Errorf("scan principal permission: %w", err)
			}
			principal.Permissions[permission] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate principal permissions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE auth_sessions
   SET last_seen_at = now()
 WHERE tenant_id = $1 AND id = $2`, tenantID, principal.SessionID); err != nil {
			return fmt.Errorf("update session last seen: %w", err)
		}
		return nil
	})
	return principal, found, err
}

func (s *PostgresStore) InvalidateSession(ctx context.Context, tenantID string, tokenHash []byte) error {
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
UPDATE auth_sessions
   SET invalidated_at = COALESCE(invalidated_at, now())
 WHERE tenant_id = $1 AND token_hash = $2`, tenantID, tokenHash); err != nil {
			return fmt.Errorf("invalidate auth session: %w", err)
		}
		return nil
	})
}

func (s *PostgresStore) UpdatePasswordHash(ctx context.Context, tenantID, userID, passwordHash string) error {
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `
UPDATE users
   SET password_hash = $3, password_params = '{}'::jsonb, updated_at = now()
 WHERE tenant_id = $1 AND id = $2`, tenantID, userID, passwordHash)
		if err != nil {
			return fmt.Errorf("update password hash: %w", err)
		}
		if command.RowsAffected() != 1 {
			return fmt.Errorf("update password hash: user not found")
		}
		return nil
	})
}

// PrepareEmailRegistration creates an unverified customer identity or lets its
// owner restart registration with a new password. A verified account is never
// altered by this public path, so it cannot become an account-takeover route.
func (s *PostgresStore) PrepareEmailRegistration(ctx context.Context, tenantID, email, passwordHash string) error {
	if tenantID == "" || email == "" || passwordHash == "" {
		return ErrInvalidAccountInput
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO users (tenant_id, email, password_hash, password_params, status)
VALUES ($1, $2, $3, '{}'::jsonb, 'ACTIVE')
ON CONFLICT (tenant_id, email) WHERE email IS NOT NULL
DO UPDATE SET password_hash = EXCLUDED.password_hash,
              password_params = '{}'::jsonb,
              updated_at = now()
      WHERE users.email_verified_at IS NULL`, tenantID, email, passwordHash); err != nil {
			return fmt.Errorf("prepare e-mail registration: %w", err)
		}
		return nil
	})
}

// VerifyEmailAndEnsureCustomer promotes a code-proven user to a customer in
// one tenant transaction. Customer linking is idempotent and the unique index
// introduced with this workflow prevents duplicate profiles during retries.
func (s *PostgresStore) VerifyEmailAndEnsureCustomer(ctx context.Context, tenantID, email string) error {
	if tenantID == "" || email == "" {
		return ErrInvalidAccountInput
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var userID string
		transitioned := false
		err := tx.QueryRow(ctx, `
UPDATE users
   SET email_verified_at = now(), updated_at = now()
 WHERE tenant_id = $1
   AND email = $2
   AND email_verified_at IS NULL
RETURNING id::text`, tenantID, email).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
SELECT id::text
  FROM users
 WHERE tenant_id = $1
   AND email = $2
   AND email_verified_at IS NOT NULL`, tenantID, email).Scan(&userID)
		} else if err == nil {
			transitioned = true
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("verified e-mail registration user was not found")
		}
		if err != nil {
			return fmt.Errorf("mark e-mail verified: %w", err)
		}

		var customerID string
		if err := tx.QueryRow(ctx, `
INSERT INTO customers (tenant_id, user_id, customer_number, status, email)
VALUES ($1, $2, 'CUS-' || replace(gen_random_uuid()::text, '-', ''), 'ACTIVE', $3)
ON CONFLICT (tenant_id, user_id) WHERE user_id IS NOT NULL
DO UPDATE SET email = EXCLUDED.email, updated_at = now()
RETURNING id::text`, tenantID, userID, email).Scan(&customerID); err != nil {
			return fmt.Errorf("ensure customer profile: %w", err)
		}
		if transitioned {
			if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id)
VALUES ($1, 'USER', $2, 'CUSTOMER_EMAIL_VERIFIED', 'customers', $3)`, tenantID, userID, customerID); err != nil {
				return fmt.Errorf("write e-mail verification audit record: %w", err)
			}
		}
		return nil
	})
}

// ResetVerifiedPassword is intentionally a no-op for an unknown or
// unverified e-mail address. Its public caller sends the same success response
// in both cases, which prevents password-reset account enumeration.
func (s *PostgresStore) ResetVerifiedPassword(ctx context.Context, tenantID, email, passwordHash string) error {
	if tenantID == "" || email == "" || passwordHash == "" {
		return ErrInvalidAccountInput
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var userID string
		err := tx.QueryRow(ctx, `
UPDATE users
   SET password_hash = $3, password_params = '{}'::jsonb, updated_at = now()
 WHERE tenant_id = $1
   AND email = $2
   AND email_verified_at IS NOT NULL
RETURNING id::text`, tenantID, email, passwordHash).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reset verified password: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE auth_sessions
   SET invalidated_at = COALESCE(invalidated_at, now())
 WHERE tenant_id = $1
   AND user_id = $2
   AND invalidated_at IS NULL`, tenantID, userID); err != nil {
			return fmt.Errorf("invalidate password-reset sessions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id)
VALUES ($1, 'USER', $2, 'CUSTOMER_PASSWORD_RESET', 'users', $2)`, tenantID, userID); err != nil {
			return fmt.Errorf("write password-reset audit record: %w", err)
		}
		return nil
	})
}

// ActiveTOTPDevice returns the user's active MFA metadata. secret_ref is only
// passed to the configured secret resolver; it is never returned by HTTP code.
func (s *PostgresStore) ActiveTOTPDevice(ctx context.Context, tenantID, userID string) (device TOTPDevice, found bool, err error) {
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, user_id::text, secret_ref, last_used_counter
  FROM user_mfa_totp
 WHERE tenant_id = $1
   AND user_id = $2
   AND status = 'ACTIVE'
 LIMIT 1`, tenantID, userID).Scan(
			&device.ID, &device.TenantID, &device.UserID, &device.SecretRef, &device.LastUsedCounter,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("query active TOTP device: %w", err)
		}
		found = true
		return nil
	})
	return device, found, err
}

// ConsumeTOTPCounter rejects an equal or older counter in the same tenant
// transaction. The conditional update makes a TOTP code single-use even when
// two API instances verify it concurrently.
func (s *PostgresStore) ConsumeTOTPCounter(ctx context.Context, device TOTPDevice, counter int64) (bool, error) {
	if counter < 0 {
		return false, errors.New("auth: TOTP counter must be non-negative")
	}
	consumed := false
	err := s.db.InTenantTx(ctx, device.TenantID, func(tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `
UPDATE user_mfa_totp
   SET last_used_counter = $4, updated_at = now()
 WHERE id = $1
   AND tenant_id = $2
   AND user_id = $3
   AND status = 'ACTIVE'
   AND last_used_counter < $4`, device.ID, device.TenantID, device.UserID, counter)
		if err != nil {
			return fmt.Errorf("consume TOTP counter: %w", err)
		}
		if command.RowsAffected() != 1 {
			return nil
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id)
VALUES ($1, 'USER', $2, 'MFA_TOTP_VERIFIED', 'user_mfa_totp', $3)`,
			device.TenantID, device.UserID, device.ID,
		); err != nil {
			return fmt.Errorf("write MFA audit record: %w", err)
		}
		consumed = true
		return nil
	})
	return consumed, err
}

// NormalizeLoginIdentifier uses the same normalization that the database's
// CITEXT comparison provides for e-mail, while keeping phone numbers untouched
// except for surrounding whitespace. E.164 canonicalization is added with OTP.
func NormalizeLoginIdentifier(identifier string) string {
	identifier = strings.TrimSpace(identifier)
	if strings.Contains(identifier, "@") {
		return strings.ToLower(identifier)
	}
	return identifier
}
