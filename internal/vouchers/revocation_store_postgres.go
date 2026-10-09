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
		if status != "UNUSED" {
			return ErrStaffVoucherDenied
		}
		if _, err := tx.Exec(ctx, `UPDATE vouchers SET status='REVOKED' WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='UNUSED'`, tenantID, voucherID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_type,actor_id,action,resource_type,resource_id,metadata) VALUES($1::uuid,'STAFF',$2::uuid,'VOUCHER_REVOKED','vouchers',$3::uuid,jsonb_build_object('previous_status',$4::text))`, tenantID, actorID, voucherID, status)
		return err
	})
}
