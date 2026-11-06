package vouchers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *PostgresStore) RedeemForDevice(ctx context.Context, tenantID string, digest [32]byte, mac, nasAddress string) (result Redemption, err error) {
	if s == nil || !validUUID(tenantID) || len(mac) != 12 || nasAddress == "" {
		return Redemption{}, ErrRedeemDenied
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var nasID string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM nas WHERE tenant_id=$1::uuid AND hotspot_address=$2::inet AND status='ACTIVE'`, tenantID, nasAddress).Scan(&nasID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrVoucherHotspotUnavailable
			}
			return fmt.Errorf("voucher redemption: resolve active hotspot: %w", err)
		}
		_ = nasID
		var voucherID, planID, status string
		var expiresAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `SELECT id::text,plan_id::text,status,expires_at FROM vouchers WHERE tenant_id=$1::uuid AND code_hash=$2 FOR UPDATE`, tenantID, digest[:]).Scan(&voucherID, &planID, &status, &expiresAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrVoucherNotFound
			}
			return fmt.Errorf("voucher redemption: lock voucher: %w", err)
		}
		if status == "REDEEMED" {
			return existingVoucherEntitlement(ctx, tx, tenantID, voucherID, mac, &result)
		}
		if status == "REVOKED" {
			return ErrVoucherRevoked
		}
		if expiresAt.Valid && !expiresAt.Time.After(time.Now()) {
			return ErrVoucherExpired
		}
		if status != "UNUSED" {
			return ErrRedeemDenied
		}
		var customerID, deviceID string
		if err := tx.QueryRow(ctx, `SELECT c.id::text,d.id::text FROM devices d JOIN customers c ON c.id=d.customer_id AND c.tenant_id=d.tenant_id WHERE d.tenant_id=$1::uuid AND d.normalized_mac=$2 AND d.status='ACTIVE' AND c.status='ACTIVE' FOR UPDATE OF d,c`, tenantID, mac).Scan(&customerID, &deviceID); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("voucher redemption: resolve device: %w", err)
			}
			if err := tx.QueryRow(ctx, `INSERT INTO customers(tenant_id,customer_number,status,is_guest) VALUES($1::uuid,'GST-'||replace(gen_random_uuid()::text,'-',''),'ACTIVE',true) RETURNING id::text`, tenantID).Scan(&customerID); err != nil {
				return fmt.Errorf("voucher redemption: create guest customer: %w", err)
			}
			if err := tx.QueryRow(ctx, `INSERT INTO devices(tenant_id,customer_id,mac_address,normalized_mac,status) VALUES($1::uuid,$2::uuid,$3,$3,'ACTIVE') RETURNING id::text`, tenantID, customerID, mac).Scan(&deviceID); err != nil {
				return fmt.Errorf("voucher redemption: create device: %w", err)
			}
		}
		if err := createBearerVoucherEntitlement(ctx, tx, tenantID, voucherID, planID, customerID, deviceID, &result); err != nil {
			return fmt.Errorf("voucher redemption: create entitlement: %w", err)
		}
		return nil
	})
	return result, err
}

func createBearerVoucherEntitlement(ctx context.Context, tx pgx.Tx, tenantID, voucherID, planID, customerID, deviceID string, result *Redemption) error {
	var duration, quota int64
	if err := tx.QueryRow(ctx, `SELECT duration_seconds,COALESCE(quota_bytes,0) FROM plans WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='ACTIVE' FOR UPDATE`, tenantID, planID).Scan(&duration, &quota); err != nil {
		return ErrVoucherPlanUnavailable
	}
	var status string
	if err := tx.QueryRow(ctx, `WITH schedule AS (SELECT GREATEST(now(),COALESCE(MAX(expires_at) FILTER (WHERE status IN ('ACTIVE','PENDING') AND expires_at>now()),now())) AS starts_at FROM subscriptions WHERE tenant_id=$1::uuid AND device_id=$4::uuid) INSERT INTO subscriptions(tenant_id,customer_id,plan_id,device_id,voucher_id,status,payment_status,starts_at,expires_at) SELECT $1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,CASE WHEN schedule.starts_at>now() THEN 'PENDING' ELSE 'ACTIVE' END,'PAID',schedule.starts_at,schedule.starts_at+$6::bigint*interval '1 second' FROM schedule RETURNING id::text,status,starts_at,expires_at`, tenantID, customerID, planID, deviceID, voucherID, duration).Scan(&result.SubscriptionID, &status, &result.StartsAt, &result.ExpiresAt); err != nil {
		return err
	}
	result.Queued = status == "PENDING"
	if _, err := tx.Exec(ctx, `UPDATE vouchers SET status='REDEEMED',redeemed_at=now(),redeemed_by=$3::uuid WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='UNUSED'`, tenantID, voucherID, customerID); err != nil {
		return err
	}
	reason := "VOUCHER_REDEEMED"
	if result.Queued {
		reason = "VOUCHER_QUEUED"
	} else {
		if _, err := tx.Exec(ctx, `INSERT INTO usage_counters(tenant_id,subscription_id,customer_id,period_start,period_end,quota_bytes) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6)`, tenantID, result.SubscriptionID, customerID, result.StartsAt, result.ExpiresAt, quota); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,tenant_id,aggregate_type,aggregate_id,event_type,payload) VALUES($1::uuid,$2::uuid,'subscription',$3::uuid,'subscription.activated',jsonb_build_object('subscription_id',$3::text,'customer_id',$4::text,'plan_id',$5::text,'activation_reason','VOUCHER','period_start',$6,'period_end',$7,'quota_bytes',$8))`, uuid.NewString(), tenantID, result.SubscriptionID, customerID, planID, result.StartsAt, result.ExpiresAt, quota); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subscription_events(tenant_id,subscription_id,from_status,to_status,reason,actor_type,metadata) VALUES($1::uuid,$2::uuid,NULL,$3,$4,'SYSTEM',jsonb_build_object('voucher_id',$5::text,'redemption_mode','BEARER'))`, tenantID, result.SubscriptionID, status, reason, voucherID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_type,action,resource_type,resource_id,metadata) VALUES($1::uuid,'SYSTEM',$2,'vouchers',$3::uuid,jsonb_build_object('subscription_id',$4::text,'device_id',$5::text,'redemption_mode','BEARER'))`, tenantID, reason, voucherID, result.SubscriptionID, deviceID)
	return err
}

// ActivateDue turns queued voucher subscriptions into normal active
// subscriptions. Expiry runs immediately before this processor in the worker,
// preventing overlap at a plan boundary.
func (s *PostgresStore) ActivateDue(ctx context.Context, tenantID string, at time.Time) (count int, err error) {
	if s == nil || !validUUID(tenantID) {
		return 0, ErrRedeemDenied
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
UPDATE subscriptions
   SET status='ACTIVE',updated_at=$2
 WHERE tenant_id=$1::uuid AND status='PENDING' AND voucher_id IS NOT NULL
   AND starts_at <= $2 AND expires_at > $2
RETURNING id::text,customer_id::text,plan_id::text,starts_at,expires_at`, tenantID, at)
		if err != nil {
			return err
		}
		type dueSubscription struct {
			id, customerID, planID string
			startsAt, expiresAt    time.Time
		}
		var due []dueSubscription
		for rows.Next() {
			var entry dueSubscription
			if err := rows.Scan(&entry.id, &entry.customerID, &entry.planID, &entry.startsAt, &entry.expiresAt); err != nil {
				rows.Close()
				return err
			}
			due = append(due, entry)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, entry := range due {
			var quota int64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(quota_bytes,0) FROM plans WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, entry.planID).Scan(&quota); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO usage_counters(tenant_id,subscription_id,customer_id,period_start,period_end,quota_bytes) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6) ON CONFLICT DO NOTHING`, tenantID, entry.id, entry.customerID, entry.startsAt, entry.expiresAt, quota); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO subscription_events(tenant_id,subscription_id,from_status,to_status,reason,actor_type,metadata) VALUES($1::uuid,$2::uuid,'PENDING','ACTIVE','VOUCHER_START','SYSTEM','{}'::jsonb)`, tenantID, entry.id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,tenant_id,aggregate_type,aggregate_id,event_type,payload) VALUES($1::uuid,$2::uuid,'subscription',$3::uuid,'subscription.activated',jsonb_build_object('subscription_id',$3::text,'customer_id',$4::text,'plan_id',$5::text,'activation_reason','VOUCHER','period_start',$6,'period_end',$7,'quota_bytes',$8))`, uuid.NewString(), tenantID, entry.id, entry.customerID, entry.planID, entry.startsAt, entry.expiresAt, quota); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func existingVoucherEntitlement(ctx context.Context, tx pgx.Tx, tenantID, voucherID, mac string, result *Redemption) error {
	var status, deviceMAC string
	var quota, consumed *int64
	err := tx.QueryRow(ctx, `SELECT s.id::text,s.status,d.normalized_mac,s.starts_at,s.expires_at,uc.quota_bytes,uc.consumed_bytes FROM subscriptions s JOIN customers c ON c.id=s.customer_id AND c.tenant_id=s.tenant_id AND c.status='ACTIVE' JOIN devices d ON d.id=s.device_id AND d.tenant_id=s.tenant_id AND d.customer_id=c.id AND d.status='ACTIVE' LEFT JOIN LATERAL (SELECT quota_bytes,consumed_bytes FROM usage_counters WHERE tenant_id=s.tenant_id AND subscription_id=s.id AND period_start<=now() AND period_end>now() ORDER BY period_start DESC LIMIT 1) uc ON true WHERE s.tenant_id=$1::uuid AND s.voucher_id=$2::uuid`, tenantID, voucherID).Scan(&result.SubscriptionID, &status, &deviceMAC, &result.StartsAt, &result.ExpiresAt, &quota, &consumed)
	if err != nil || deviceMAC != mac || (status != "ACTIVE" && status != "PENDING") {
		return ErrVoucherAlreadyRedeemed
	}
	result.Queued = status == "PENDING"
	if result.Queued {
		return nil
	}
	if result.StartsAt.After(time.Now()) || !result.ExpiresAt.After(time.Now()) || quota == nil || consumed == nil || (*quota > 0 && *consumed >= *quota) {
		return ErrVoucherExpired
	}
	return nil
}
