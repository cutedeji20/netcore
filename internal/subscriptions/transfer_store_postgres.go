package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Transfer changes only the device binding of an existing subscription.
// Open sessions block the move because a database update cannot disconnect a
// client already authorized by RouterOS.
func (s *PostgresStore) Transfer(ctx context.Context, tenantID string, actor GrantActor, subscriptionID, targetDeviceID, reason string) error {
	reason = strings.TrimSpace(reason)
	if !validUUID(tenantID) || !validUUID(actor.UserID) || !validUUID(subscriptionID) || !validUUID(targetDeviceID) || reason == "" || len(reason) > 240 {
		return ErrTransferNotEligible
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var customerID, oldDeviceID string
		var planQuota *int64
		err := tx.QueryRow(ctx, `
SELECT s.customer_id::text, COALESCE(s.device_id::text,''), p.quota_bytes
  FROM subscriptions AS s
  JOIN customers AS c ON c.id=s.customer_id AND c.tenant_id=s.tenant_id AND c.status='ACTIVE'
  JOIN plans AS p ON p.id=s.plan_id AND p.tenant_id=s.tenant_id AND p.status='ACTIVE'
 WHERE s.tenant_id=$1 AND s.id=$2::uuid AND s.status='ACTIVE'
   AND s.starts_at<=now() AND s.expires_at>now()
 FOR UPDATE OF s`, tenantID, subscriptionID).Scan(&customerID, &oldDeviceID, &planQuota)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTransferTargetNotFound
		}
		if err != nil {
			return fmt.Errorf("lock transfer subscription: %w", err)
		}
		if oldDeviceID == "" {
			return ErrTransferNotEligible
		}
		if oldDeviceID == targetDeviceID {
			return nil
		}
		var pending bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM device_replacements WHERE tenant_id=$1::uuid AND subscription_id=$2::uuid AND status IN ('CHALLENGE','PENDING') AND expires_at>now())`, tenantID, subscriptionID).Scan(&pending); err != nil {
			return fmt.Errorf("check pending device replacement: %w", err)
		}
		if pending {
			return ErrTransferNotEligible
		}
		var targetMAC string
		err = tx.QueryRow(ctx, `SELECT normalized_mac FROM devices WHERE tenant_id=$1 AND id=$2::uuid AND customer_id=$3::uuid AND status='ACTIVE' AND normalized_mac IS NOT NULL FOR UPDATE`, tenantID, targetDeviceID, customerID).Scan(&targetMAC)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTransferTargetNotFound
		}
		if err != nil {
			return fmt.Errorf("check transfer device: %w", err)
		}
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE tenant_id=$1 AND subscription_id=$2::uuid AND status<>'CLOSED') OR subscription_has_radius_reservation($1::uuid,$2::uuid)`, tenantID, subscriptionID).Scan(&open); err != nil {
			return fmt.Errorf("check transfer sessions: %w", err)
		}
		if open {
			return ErrTransferHasOpenSession
		}
		if planQuota != nil {
			var quotaAvailable bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM usage_counters WHERE tenant_id=$1 AND subscription_id=$2::uuid AND period_start<=now() AND period_end>now() AND exhausted_at IS NULL AND consumed_bytes<quota_bytes)`, tenantID, subscriptionID).Scan(&quotaAvailable); err != nil {
				return fmt.Errorf("check transfer quota: %w", err)
			}
			if !quotaAvailable {
				return ErrTransferNotEligible
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET device_id=$3::uuid,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, subscriptionID, targetDeviceID); err != nil {
			return fmt.Errorf("transfer subscription device: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscription_events (tenant_id,subscription_id,from_status,to_status,reason,actor_type,actor_id,metadata) VALUES ($1,$2::uuid,'ACTIVE','ACTIVE','STAFF_DEVICE_TRANSFER','ADMIN',$3::uuid,jsonb_build_object('reason',$4::text,'old_device_id',$5::uuid,'new_device_id',$6::uuid))`, tenantID, subscriptionID, actor.UserID, reason, oldDeviceID, targetDeviceID); err != nil {
			return fmt.Errorf("audit subscription device transfer: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (tenant_id,actor_type,actor_id,action,resource_type,resource_id,ip_address,user_agent,metadata) VALUES ($1,'STAFF',$2::uuid,'SUBSCRIPTION_DEVICE_TRANSFERRED','subscription',$3::uuid,NULLIF($4,'')::inet,NULLIF($5,''),jsonb_build_object('reason',$6::text,'old_device_id',$7::uuid,'new_device_id',$8::uuid))`, tenantID, actor.UserID, subscriptionID, actor.IPAddress, actor.UserAgent, reason, oldDeviceID, targetDeviceID); err != nil {
			return fmt.Errorf("audit staff device transfer: %w", err)
		}
		return nil
	})
}
