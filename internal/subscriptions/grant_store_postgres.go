package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Grant creates a normal, device-bound active subscription. It never creates
// an unrestricted bypass: the selected published plan still supplies its
// expiry and quota terms.
func (s *PostgresStore) Grant(ctx context.Context, tenantID string, actor GrantActor, input GrantInput) (subscription Subscription, err error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if !validUUID(tenantID) || !validUUID(actor.UserID) || !validUUID(input.CustomerID) || !validUUID(input.PlanID) || !validUUID(input.DeviceID) || input.Reason == "" || len(input.Reason) > 240 {
		return Subscription{}, ErrInvalidGrant
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var durationSeconds int64
		var quotaBytes int64
		var customerOK, planOK, deviceOK bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customers WHERE tenant_id=$1 AND id=$2::uuid AND status='ACTIVE'), EXISTS(SELECT 1 FROM plans WHERE tenant_id=$1 AND id=$3::uuid AND status='ACTIVE'), EXISTS(SELECT 1 FROM devices WHERE tenant_id=$1 AND id=$4::uuid AND customer_id=$2::uuid AND status='ACTIVE')`, tenantID, input.CustomerID, input.PlanID, input.DeviceID).Scan(&customerOK, &planOK, &deviceOK); err != nil {
			return fmt.Errorf("subscriptions: validate grant: %w", err)
		}
		if !customerOK || !planOK || !deviceOK {
			return ErrGrantTargetNotFound
		}
		if err := tx.QueryRow(ctx, `SELECT duration_seconds, COALESCE(quota_bytes, 0) FROM plans WHERE tenant_id=$1 AND id=$2::uuid AND status='ACTIVE'`, tenantID, input.PlanID).Scan(&durationSeconds, &quotaBytes); err != nil {
			return fmt.Errorf("subscriptions: read grant plan: %w", err)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO subscriptions (tenant_id,customer_id,plan_id,device_id,status,payment_status,starts_at,expires_at) VALUES ($1,$2::uuid,$3::uuid,$4::uuid,'ACTIVE','GRANTED',now(),now()+$5*interval '1 second') RETURNING id::text, customer_id::text, plan_id::text, status, starts_at, expires_at, auto_renew, payment_status, created_at, updated_at`, tenantID, input.CustomerID, input.PlanID, input.DeviceID, durationSeconds).Scan(&subscription.ID, &subscription.CustomerID, &subscription.PlanID, &subscription.Status, &subscription.StartsAt, &subscription.ExpiresAt, &subscription.AutoRenew, &subscription.PaymentStatus, &subscription.CreatedAt, &subscription.UpdatedAt); err != nil {
			return fmt.Errorf("subscriptions: create grant: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO usage_counters (tenant_id,subscription_id,customer_id,period_start,period_end,quota_bytes) VALUES ($1,$2::uuid,$3::uuid,$4,$5,$6)`, tenantID, subscription.ID, input.CustomerID, *subscription.StartsAt, *subscription.ExpiresAt, quotaBytes); err != nil {
			return fmt.Errorf("subscriptions: create grant usage: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscription_events (tenant_id,subscription_id,from_status,to_status,reason,actor_type,actor_id,metadata) VALUES ($1,$2::uuid,NULL,'ACTIVE','STAFF_GRANT','ADMIN',$3::uuid,jsonb_build_object('reason',$4::text))`, tenantID, subscription.ID, actor.UserID, input.Reason); err != nil {
			return fmt.Errorf("subscriptions: audit grant event: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (tenant_id,actor_type,actor_id,action,resource_type,resource_id,ip_address,user_agent,metadata) VALUES ($1,'STAFF',$2::uuid,'SUBSCRIPTION_GRANTED_BY_STAFF','subscription',$3::uuid,NULLIF($4,'')::inet,NULLIF($5,''),jsonb_build_object('customer_id',$6::uuid,'plan_id',$7::uuid,'device_id',$8::uuid,'reason',$9::text))`, tenantID, actor.UserID, subscription.ID, actor.IPAddress, actor.UserAgent, input.CustomerID, input.PlanID, input.DeviceID, input.Reason); err != nil {
			return fmt.Errorf("subscriptions: audit grant: %w", err)
		}
		return nil
	})
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, ErrGrantTargetNotFound
	}
	return subscription, err
}
