package vouchers

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *PostgresStore) RedeemForDevice(ctx context.Context, tenantID string, digest [32]byte, mac, nasAddress string) (string, error) {
	if s == nil || !validUUID(tenantID) || len(mac) != 12 || nasAddress == "" {
		return "", ErrRedeemDenied
	}
	var subscriptionID string
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var nasID string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM nas WHERE tenant_id=$1::uuid AND hotspot_address=$2::inet AND status='ACTIVE'`, tenantID, nasAddress).Scan(&nasID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRedeemDenied
			}
			return err
		}
		_ = nasID
		var voucherID, planID, status string
		var expiresAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `SELECT id::text,plan_id::text,status,expires_at FROM vouchers WHERE tenant_id=$1::uuid AND code_hash=$2 FOR UPDATE`, tenantID, digest[:]).Scan(&voucherID, &planID, &status, &expiresAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRedeemDenied
			}
			return err
		}
		if status == "REDEEMED" {
			return existingVoucherEntitlement(ctx, tx, tenantID, voucherID, mac, &subscriptionID)
		}
		if status != "UNUSED" || (expiresAt.Valid && !expiresAt.Time.After(time.Now())) {
			return ErrRedeemDenied
		}
		var existingDevice bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE tenant_id=$1::uuid AND normalized_mac=$2 AND status<>'REMOVED')`, tenantID, mac).Scan(&existingDevice); err != nil {
			return err
		}
		if existingDevice {
			return ErrExistingDevice
		}
		var duration, quota int64
		if err := tx.QueryRow(ctx, `SELECT duration_seconds,COALESCE(quota_bytes,0) FROM plans WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, planID).Scan(&duration, &quota); err != nil {
			return ErrRedeemDenied
		}
		var customerID, deviceID string
		if err := tx.QueryRow(ctx, `INSERT INTO customers(tenant_id,customer_number,status,is_guest) VALUES($1::uuid,'GST-'||replace(gen_random_uuid()::text,'-',''),'ACTIVE',true) RETURNING id::text`, tenantID).Scan(&customerID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO devices(tenant_id,customer_id,mac_address,normalized_mac,status) VALUES($1::uuid,$2::uuid,$3,$3,'ACTIVE') RETURNING id::text`, tenantID, customerID, mac).Scan(&deviceID); err != nil {
			return err
		}
		var startsAt, endsAt time.Time
		if err := tx.QueryRow(ctx, `INSERT INTO subscriptions(tenant_id,customer_id,plan_id,device_id,voucher_id,status,payment_status,starts_at,expires_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'ACTIVE','PAID',now(),now()+$6::bigint*interval '1 second') RETURNING id::text,starts_at,expires_at`, tenantID, customerID, planID, deviceID, voucherID, duration).Scan(&subscriptionID, &startsAt, &endsAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO usage_counters(tenant_id,subscription_id,customer_id,period_start,period_end,quota_bytes) VALUES($1::uuid,$2::uuid,$3::uuid,$4::timestamptz,$5::timestamptz,$6::bigint)`, tenantID, subscriptionID, customerID, startsAt, endsAt, quota); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE vouchers SET status='REDEEMED',redeemed_at=$3::timestamptz,redeemed_by=$4::uuid WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='UNUSED'`, tenantID, voucherID, startsAt, customerID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscription_events(tenant_id,subscription_id,from_status,to_status,reason,actor_type,metadata) VALUES($1::uuid,$2::uuid,NULL,'ACTIVE','VOUCHER','SYSTEM',jsonb_build_object('voucher_id',$3::text))`, tenantID, subscriptionID, voucherID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,tenant_id,aggregate_type,aggregate_id,event_type,payload) VALUES($1::uuid,$2::uuid,'subscription',$3::uuid,'subscription.activated',jsonb_build_object('subscription_id',$3::text,'customer_id',$4::text,'plan_id',$5::text,'activation_reason','VOUCHER','period_start',$6::timestamptz,'period_end',$7::timestamptz,'quota_bytes',$8::bigint))`, uuid.NewString(), tenantID, subscriptionID, customerID, planID, startsAt, endsAt, quota); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_type,action,resource_type,resource_id,metadata) VALUES($1::uuid,'SYSTEM','VOUCHER_REDEEMED','vouchers',$2::uuid,jsonb_build_object('subscription_id',$3::text))`, tenantID, voucherID, subscriptionID)
		return err
	})
	if err != nil {
		return "", err
	}
	return subscriptionID, nil
}

// RedeemForCustomerDevice attaches a paid voucher only to the authenticated
// customer's own enrolled device. Device row locking serializes claims for a
// MAC, so two browser requests cannot create overlapping active entitlements.
func (s *PostgresStore) RedeemForCustomerDevice(ctx context.Context, tenantID, userID string, digest [32]byte, mac, nasAddress string) (result Redemption, err error) {
	if s == nil || !validUUID(tenantID) || !validUUID(userID) || len(mac) != 12 || nasAddress == "" {
		return Redemption{}, ErrRedeemDenied
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var nasID, customerID, deviceID string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM nas WHERE tenant_id=$1::uuid AND hotspot_address=$2::inet AND status='ACTIVE'`, tenantID, nasAddress).Scan(&nasID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRedeemDenied
			}
			return err
		}
		if err := tx.QueryRow(ctx, `
SELECT c.id::text,d.id::text
  FROM devices d
  JOIN customers c ON c.id=d.customer_id AND c.tenant_id=d.tenant_id
 WHERE d.tenant_id=$1::uuid AND d.normalized_mac=$2
   AND d.status='ACTIVE' AND c.status='ACTIVE' AND c.user_id=$3::uuid
 FOR UPDATE OF d,c`, tenantID, mac, userID).Scan(&customerID, &deviceID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRedeemDenied
			}
			return err
		}
		_ = nasID

		var voucherID, planID, status string
		var expiresAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `SELECT id::text,plan_id::text,status,expires_at FROM vouchers WHERE tenant_id=$1::uuid AND code_hash=$2 FOR UPDATE`, tenantID, digest[:]).Scan(&voucherID, &planID, &status, &expiresAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRedeemDenied
			}
			return err
		}
		if status == "REDEEMED" {
			var redeemedCustomerID, redeemedDeviceID, redeemedStatus string
			if err := tx.QueryRow(ctx, `SELECT id::text,customer_id::text,device_id::text,status,starts_at,expires_at FROM subscriptions WHERE tenant_id=$1::uuid AND voucher_id=$2::uuid`, tenantID, voucherID).Scan(&result.SubscriptionID, &redeemedCustomerID, &redeemedDeviceID, &redeemedStatus, &result.StartsAt, &result.ExpiresAt); err != nil || redeemedCustomerID != customerID || redeemedDeviceID != deviceID || (redeemedStatus != "ACTIVE" && redeemedStatus != "PENDING") {
				return ErrRedeemDenied
			}
			result.Queued = redeemedStatus == "PENDING"
			return nil
		}
		if status != "UNUSED" || (expiresAt.Valid && !expiresAt.Time.After(time.Now())) {
			return ErrRedeemDenied
		}

		var duration, quota int64
		if err := tx.QueryRow(ctx, `SELECT duration_seconds,COALESCE(quota_bytes,0) FROM plans WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='ACTIVE' FOR UPDATE`, tenantID, planID).Scan(&duration, &quota); err != nil {
			return ErrRedeemDenied
		}
		var subscriptionStatus string
		if err := tx.QueryRow(ctx, `
WITH schedule AS (
  SELECT GREATEST(now(), COALESCE(MAX(expires_at) FILTER (WHERE status IN ('ACTIVE','PENDING') AND expires_at>now()), now())) AS starts_at
    FROM subscriptions
   WHERE tenant_id=$1::uuid AND device_id=$4::uuid
)
INSERT INTO subscriptions(tenant_id,customer_id,plan_id,device_id,voucher_id,status,payment_status,starts_at,expires_at)
SELECT $1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,
       CASE WHEN schedule.starts_at>now() THEN 'PENDING' ELSE 'ACTIVE' END,
       'PAID',schedule.starts_at,schedule.starts_at+$6::bigint*interval '1 second'
  FROM schedule
RETURNING id::text,status,starts_at,expires_at`, tenantID, customerID, planID, deviceID, voucherID, duration).Scan(&result.SubscriptionID, &subscriptionStatus, &result.StartsAt, &result.ExpiresAt); err != nil {
			return err
		}
		result.Queued = subscriptionStatus == "PENDING"
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
		if _, err := tx.Exec(ctx, `INSERT INTO subscription_events(tenant_id,subscription_id,from_status,to_status,reason,actor_type,actor_id,metadata) VALUES($1::uuid,$2::uuid,NULL,$3,$4,'CUSTOMER',$5::uuid,jsonb_build_object('voucher_id',$6::text))`, tenantID, result.SubscriptionID, subscriptionStatus, reason, userID, voucherID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_type,actor_id,action,resource_type,resource_id,metadata) VALUES($1::uuid,'CUSTOMER',$2::uuid,$3,'vouchers',$4::uuid,jsonb_build_object('subscription_id',$5::text,'device_id',$6::text))`, tenantID, userID, reason, voucherID, result.SubscriptionID, deviceID)
		return err
	})
	return result, err
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

func existingVoucherEntitlement(ctx context.Context, tx pgx.Tx, tenantID, voucherID, mac string, subscriptionID *string) error {
	var status, deviceMAC string
	var startsAt, endsAt time.Time
	var quota, consumed int64
	err := tx.QueryRow(ctx, `SELECT s.id::text,s.status,d.normalized_mac,s.starts_at,s.expires_at,uc.quota_bytes,uc.consumed_bytes FROM subscriptions s JOIN customers c ON c.id=s.customer_id AND c.tenant_id=s.tenant_id AND c.is_guest JOIN devices d ON d.id=s.device_id AND d.tenant_id=s.tenant_id AND d.customer_id=c.id AND d.status='ACTIVE' JOIN usage_counters uc ON uc.subscription_id=s.id AND uc.tenant_id=s.tenant_id AND uc.period_start<=now() AND uc.period_end>now() WHERE s.tenant_id=$1::uuid AND s.voucher_id=$2::uuid ORDER BY uc.period_start DESC LIMIT 1`, tenantID, voucherID).Scan(subscriptionID, &status, &deviceMAC, &startsAt, &endsAt, &quota, &consumed)
	if err != nil || status != "ACTIVE" || deviceMAC != mac || startsAt.After(time.Now()) || !endsAt.After(time.Now()) || (quota > 0 && consumed >= quota) {
		return ErrRedeemDenied
	}
	return nil
}
