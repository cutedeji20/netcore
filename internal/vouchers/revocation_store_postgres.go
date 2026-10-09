package vouchers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) RevokeVoucher(ctx context.Context, tenantID, voucherID, actorID string) error {
	if !validUUID(tenantID) || !validUUID(voucherID) || !validUUID(actorID) {
		return ErrStaffVoucherDenied
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var administrator bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id AND r.tenant_id=u.tenant_id WHERE u.tenant_id=$1::uuid AND u.id=$2::uuid AND u.status='ACTIVE' AND r.name='Administrator')`, tenantID, actorID).Scan(&administrator); err != nil {
			return err
		}
		if !administrator {
			return ErrStaffVoucherDenied
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM vouchers WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, voucherID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrStaffVoucherDenied
			}
			return err
		}
		if status == "REVOKED" {
			return nil
		}
		if status != "UNUSED" && status != "REDEEMED" {
			return ErrStaffVoucherDenied
		}
		if status == "REDEEMED" {
			var subscriptionID, subscriptionStatus string
			if err := tx.QueryRow(ctx, `SELECT id::text,status FROM subscriptions WHERE tenant_id=$1::uuid AND voucher_id=$2::uuid FOR UPDATE`, tenantID, voucherID).Scan(&subscriptionID, &subscriptionStatus); err != nil {
				return err
			}
			var open bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE tenant_id=$1::uuid AND subscription_id=$2::uuid AND status<>'CLOSED') OR subscription_has_radius_reservation($1::uuid,$2::uuid)`, tenantID, subscriptionID).Scan(&open); err != nil {
				return err
			}
			if open {
				return ErrVoucherHasOpenSession
			}
			if subscriptionStatus == "ACTIVE" || subscriptionStatus == "SUSPENDED" {
				if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status='CANCELLED',auto_renew=false,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, subscriptionID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO subscription_events(tenant_id,subscription_id,from_status,to_status,reason,actor_type,actor_id,metadata) VALUES($1::uuid,$2::uuid,$3,'CANCELLED','VOUCHER_REVOKED','ADMIN',$4::uuid,jsonb_build_object('voucher_id',$5::text))`, tenantID, subscriptionID, subscriptionStatus, actorID, voucherID); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE vouchers SET status='REVOKED' WHERE tenant_id=$1::uuid AND id=$2::uuid AND status IN ('UNUSED','REDEEMED')`, tenantID, voucherID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_type,actor_id,action,resource_type,resource_id,metadata) VALUES($1::uuid,'STAFF',$2::uuid,'VOUCHER_REVOKED','vouchers',$3::uuid,jsonb_build_object('previous_status',$4::text))`, tenantID, actorID, voucherID, status)
		return err
	})
}
