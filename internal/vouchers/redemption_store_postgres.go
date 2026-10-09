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
			return ErrRedeemDenied
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
