package team

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/netcore-isp/netcore/internal/auth"
)

const existingTenantUserByEmailSQL = `SELECT id::text FROM users WHERE tenant_id=$1 AND email=$2 LIMIT 1 FOR UPDATE`
const lockedTenantStaffTargetSQL = `SELECT u.id::text FROM users u WHERE u.tenant_id=$1 AND u.id=$2 AND u.status='ACTIVE' AND EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.tenant_id=u.tenant_id WHERE ur.user_id=u.id) FOR UPDATE`
const invalidateTargetSessionsSQL = `UPDATE auth_sessions SET invalidated_at=COALESCE(invalidated_at,now()) WHERE tenant_id=$1 AND user_id=$2 AND invalidated_at IS NULL`

// CreateInvitation records a non-redeemable delivery state first.
func (s *PostgresStore) CreateInvitation(ctx context.Context, inv Invitation, digest []byte) (Invitation, error) {
	if s == nil || s.db == nil || !validUUID(inv.TenantID) || !validUUID(inv.CreatedBy) || len(digest) != 32 {
		return Invitation{}, ErrStoreUnavailable
	}
	err := s.db.InTenantTx(ctx, inv.TenantID, func(tx pgx.Tx) error {
		var userID string
		lookupErr := tx.QueryRow(ctx, existingTenantUserByEmailSQL, inv.TenantID, inv.Email).Scan(&userID)
		if lookupErr == nil {
			return ErrStaffConflict
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return lookupErr
		}
		if _, err := tx.Exec(ctx, `UPDATE staff_invitations SET status='REVOKED',updated_at=now() WHERE tenant_id=$1 AND email=$2 AND status IN ('PENDING','DELIVERY_PENDING') AND expires_at<=now()`, inv.TenantID, inv.Email); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO staff_invitations (tenant_id,email,requested_role,token_digest,status,expires_at,created_by,sent_by)
VALUES ($1,$2,$3,$4,'DELIVERY_PENDING',$5,$6::uuid,$6::uuid) RETURNING id::text`, inv.TenantID, inv.Email, inv.Role, digest, inv.ExpiresAt, inv.CreatedBy).Scan(&inv.ID); err != nil {
			return mapInviteConstraint(err)
		}
		return teamAudit(ctx, tx, inv.TenantID, inv.CreatedBy, "STAFF_INVITED", "staff_invitation", inv.ID)
	})
	if err != nil {
		return Invitation{}, err
	}
	return inv, nil
}

func (s *PostgresStore) MarkInvitationDelivered(ctx context.Context, tenantID, id, actorID string) error {
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var got string
		if err := tx.QueryRow(ctx, `UPDATE staff_invitations SET status='PENDING',sent_by=$3::uuid,updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status='DELIVERY_PENDING' AND expires_at>now() RETURNING id::text`, tenantID, id, actorID).Scan(&got); err != nil {
			return err
		}
		return teamAudit(ctx, tx, tenantID, actorID, "STAFF_INVITATION_DELIVERED", "staff_invitation", id)
	})
}

func (s *PostgresStore) ListInvitations(ctx context.Context, tenantID string) ([]Invitation, error) {
	if s == nil || s.db == nil || !validUUID(tenantID) {
		return nil, ErrStoreUnavailable
	}
	invitations := make([]Invitation, 0)
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text,tenant_id::text,email::text,requested_role,status,expires_at,created_by::text FROM staff_invitations WHERE tenant_id=$1 AND status IN ('PENDING','DELIVERY_PENDING') AND expires_at>now() ORDER BY created_at DESC`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var invitation Invitation
			if err := rows.Scan(&invitation.ID, &invitation.TenantID, &invitation.Email, &invitation.Role, &invitation.Status, &invitation.ExpiresAt, &invitation.CreatedBy); err != nil {
				return err
			}
			invitations = append(invitations, invitation)
		}
		return rows.Err()
	})
	return invitations, err
}

func (s *PostgresStore) FindInvitation(ctx context.Context, tenantID, id string) (Invitation, bool, error) {
	var inv Invitation
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return scanInvitation(tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,email::text,requested_role,status,expires_at,created_by::text FROM staff_invitations WHERE tenant_id=$1 AND id=$2`, tenantID, id), &inv)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, false, nil
	}
	return inv, err == nil, err
}

// FindInvitationByDigest starts by resolving the owning tenant from the
// digest, then immediately re-enters the tenant RLS boundary for every read.
// Production migrations expose staff_invitation_tenant_for_digest as a narrow
// SECURITY DEFINER resolver; its only output is a tenant UUID.
func (s *PostgresStore) FindInvitationByDigest(ctx context.Context, digest []byte) (Invitation, bool, error) {
	if s == nil || s.db == nil || len(digest) != 32 {
		return Invitation{}, false, ErrStoreUnavailable
	}
	var tenantID string
	err := s.db.InSystemTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT staff_invitation_tenant_for_digest($1)::text`, digest).Scan(&tenantID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, false, nil
	}
	if err != nil || !validUUID(tenantID) {
		return Invitation{}, false, err
	}
	return s.findInvitationByDigestInTenant(ctx, tenantID, digest)
}
func (s *PostgresStore) findInvitationByDigestInTenant(ctx context.Context, tenantID string, digest []byte) (Invitation, bool, error) {
	var inv Invitation
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT i.id::text,i.tenant_id::text,i.email::text,i.requested_role,i.status,i.expires_at,i.created_by::text,
m.secret_ciphertext,m.secret_nonce,m.wrapped_dek,COALESCE(m.kek_key_id,'') FROM staff_invitations i LEFT JOIN staff_invitation_mfa m ON m.tenant_id=i.tenant_id AND m.invitation_id=i.id WHERE i.tenant_id=$1 AND i.token_digest=$2`, tenantID, digest).Scan(&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Status, &inv.ExpiresAt, &inv.CreatedBy, &inv.MFA.Ciphertext, &inv.MFA.Nonce, &inv.MFA.WrappedDEK, &inv.MFA.KEKKeyID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, false, nil
	}
	return inv, err == nil, err
}

func (s *PostgresStore) CreateOrReuseInvitationMFA(ctx context.Context, inv Invitation, digest []byte, mfa auth.MFASecretEnvelope) (auth.MFASecretEnvelope, error) {
	var stored auth.MFASecretEnvelope
	err := s.db.InTenantTx(ctx, inv.TenantID, func(tx pgx.Tx) error {
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM staff_invitations WHERE tenant_id=$1 AND id=$2 AND token_digest=$3 AND status='PENDING' AND expires_at>now() FOR UPDATE`, inv.TenantID, inv.ID, digest).Scan(&locked); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT secret_ciphertext,secret_nonce,wrapped_dek,kek_key_id FROM staff_invitation_mfa WHERE tenant_id=$1 AND invitation_id=$2 FOR UPDATE`, inv.TenantID, inv.ID).Scan(&stored.Ciphertext, &stored.Nonce, &stored.WrappedDEK, &stored.KEKKeyID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO staff_invitation_mfa(invitation_id,tenant_id,secret_ciphertext,secret_nonce,wrapped_dek,kek_key_id) VALUES($1::uuid,$2,$3,$4,$5,$6)`, inv.ID, inv.TenantID, mfa.Ciphertext, mfa.Nonce, mfa.WrappedDEK, mfa.KEKKeyID); err == nil {
			stored = mfa
		}
		return err
	})
	return stored, err
}

func (s *PostgresStore) CompleteInvitation(ctx context.Context, inv Invitation, passwordHash string, mfa auth.MFASecretEnvelope) error {
	return s.db.InTenantTx(ctx, inv.TenantID, func(tx pgx.Tx) error {
		var invitationID string
		if err := tx.QueryRow(ctx, `UPDATE staff_invitations SET status='REDEEMED',redeemed_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status='PENDING' AND expires_at>now() RETURNING id::text`, inv.TenantID, inv.ID).Scan(&invitationID); err != nil {
			return err
		}
		var userID, roleID string
		if err := tx.QueryRow(ctx, `INSERT INTO users (tenant_id,email,password_hash,password_params,status,email_verified_at) VALUES ($1,$2,$3,'{}','ACTIVE',now()) RETURNING id::text`, inv.TenantID, inv.Email, passwordHash).Scan(&userID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id::text FROM roles WHERE tenant_id=$1 AND name=$2`, inv.TenantID, inv.Role).Scan(&roleID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1::uuid,$2::uuid)`, userID, roleID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_mfa_totp(tenant_id,user_id,secret_ref,secret_ciphertext,secret_nonce,wrapped_dek,kek_key_id,status,enabled_at) VALUES($1,$2::uuid,NULL,$3,$4,$5,$6,'ACTIVE',now())`, inv.TenantID, userID, mfa.Ciphertext, mfa.Nonce, mfa.WrappedDEK, mfa.KEKKeyID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM staff_invitation_mfa WHERE tenant_id=$1 AND invitation_id=$2`, inv.TenantID, inv.ID); err != nil {
			return err
		}
		return teamAudit(ctx, tx, inv.TenantID, userID, "STAFF_INVITATION_REDEEMED", "staff_invitation", inv.ID)
	})
}

func (s *PostgresStore) RevokeInvitation(ctx context.Context, tenantID, id, actorID string) error {
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var got string
		if err := tx.QueryRow(ctx, `UPDATE staff_invitations SET status='REVOKED',updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status IN ('PENDING','DELIVERY_PENDING') RETURNING id::text`, tenantID, id).Scan(&got); err != nil {
			return err
		}
		return teamAudit(ctx, tx, tenantID, actorID, "STAFF_INVITATION_REVOKED", "staff_invitation", id)
	})
}
func (s *PostgresStore) ChangeStaffRole(ctx context.Context, tenantID, actorID, targetID string, role BuiltInRole) error {
	return s.mutateStaff(ctx, tenantID, actorID, targetID, role, false)
}
func (s *PostgresStore) DeactivateStaff(ctx context.Context, tenantID, actorID, targetID string) error {
	return s.mutateStaff(ctx, tenantID, actorID, targetID, "", true)
}
func (s *PostgresStore) mutateStaff(ctx context.Context, tenantID, actorID, targetID string, role BuiltInRole, deactivate bool) error {
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenantID); err != nil {
			return err
		}
		var target string
		if err := tx.QueryRow(ctx, lockedTenantStaffTargetSQL, tenantID, targetID).Scan(&target); err != nil {
			return ErrInvitationInvalid
		}
		if deactivate {
			command, err := tx.Exec(ctx, `UPDATE users SET status='DISABLED',updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status='ACTIVE'`, tenantID, targetID)
			if err != nil {
				return err
			}
			if command.RowsAffected() != 1 {
				return ErrInvitationInvalid
			}
		} else {
			var roleID string
			if err := tx.QueryRow(ctx, `SELECT id::text FROM roles WHERE tenant_id=$1 AND name=$2`, tenantID, role).Scan(&roleID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM user_roles ur USING users u WHERE ur.user_id=u.id AND u.tenant_id=$1 AND u.id=$2`, tenantID, targetID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1::uuid,$2::uuid)`, targetID, roleID); err != nil {
				return err
			}
		}
		var admins int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id AND r.tenant_id=u.tenant_id WHERE u.tenant_id=$1 AND u.status='ACTIVE' AND r.name='Administrator'`, tenantID).Scan(&admins); err != nil {
			return err
		}
		if admins < 1 {
			return ErrLastAdministrator
		}
		if _, err := tx.Exec(ctx, invalidateTargetSessionsSQL, tenantID, targetID); err != nil {
			return err
		}
		action := "STAFF_ROLE_CHANGED"
		if deactivate {
			action = "STAFF_DEACTIVATED"
		}
		return teamAudit(ctx, tx, tenantID, actorID, action, "users", targetID)
	})
}
func scanInvitation(row pgx.Row, inv *Invitation) error {
	return row.Scan(&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Status, &inv.ExpiresAt, &inv.CreatedBy)
}
func teamAudit(ctx context.Context, tx pgx.Tx, tenantID, actorID, action, resourceType, resourceID string) error {
	metadata, err := redactedAuditMetadata()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_type,actor_id,action,resource_type,resource_id,metadata) VALUES($1,'USER',$2::uuid,$3,$4,$5::uuid,$6::jsonb)`, tenantID, actorID, action, resourceType, resourceID, metadata)
	return err
}
func redactedAuditMetadata() ([]byte, error) { return json.Marshal(map[string]string{}) }
func mapInviteConstraint(err error) error {
	var databaseErr *pgconn.PgError
	if errors.As(err, &databaseErr) && databaseErr.Code == "23505" && databaseErr.ConstraintName == "staff_invitations_one_live_email_idx" {
		return ErrStaffConflict
	}
	return err
}
