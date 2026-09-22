package subscriptions

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RevokeGrant preserves the historical grant and audit trail. Without a CoA
// dispatcher we refuse to revoke while a RADIUS session remains open.
func (s *PostgresStore) RevokeGrant(ctx context.Context, tenantID string, actor GrantActor, subscriptionID, reason string) error {
	reason = strings.TrimSpace(reason)
	if !validUUID(tenantID) || !validUUID(actor.UserID) || !validUUID(subscriptionID) || reason == "" || len(reason) > 240 {
		return ErrInvalidGrant
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var status Status
		err := tx.QueryRow(ctx, `SELECT status FROM subscriptions WHERE tenant_id=$1 AND id=$2::uuid AND payment_status='GRANTED' AND status IN ('ACTIVE','SUSPENDED') FOR UPDATE`, tenantID, subscriptionID).Scan(&status)
		if err == pgx.ErrNoRows {
			return ErrGrantTargetNotFound
		}
		if err != nil {
			return fmt.Errorf("lock staff grant: %w", err)
		}
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE tenant_id=$1 AND subscription_id=$2::uuid AND status <> 'CLOSED')`, tenantID, subscriptionID).Scan(&open); err != nil {
			return fmt.Errorf("check grant sessions: %w", err)
		}
		if open {
			return ErrGrantHasOpenSession
		}
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status='CANCELLED', auto_renew=false, updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, subscriptionID); err != nil {
			return fmt.Errorf("revoke staff grant: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscription_events (tenant_id,subscription_id,from_status,to_status,reason,actor_type,actor_id,metadata) VALUES ($1,$2::uuid,$3,'CANCELLED','STAFF_GRANT_REVOKED','ADMIN',$4::uuid,jsonb_build_object('reason',$5::text))`, tenantID, subscriptionID, status, actor.UserID, reason); err != nil {
			return fmt.Errorf("audit grant revocation event: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (tenant_id,actor_type,actor_id,action,resource_type,resource_id,ip_address,user_agent,metadata) VALUES ($1,'STAFF',$2::uuid,'SUBSCRIPTION_GRANT_REVOKED_BY_STAFF','subscription',$3::uuid,NULLIF($4,'')::inet,NULLIF($5,''),jsonb_build_object('reason',$6::text))`, tenantID, actor.UserID, subscriptionID, actor.IPAddress, actor.UserAgent, reason); err != nil {
			return fmt.Errorf("audit grant revocation: %w", err)
		}
		return nil
	})
}
