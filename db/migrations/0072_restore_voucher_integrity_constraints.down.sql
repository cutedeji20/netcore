BEGIN;

DROP INDEX IF EXISTS vouchers_tenant_batch_idx;
DROP INDEX IF EXISTS vouchers_order_position_unique;
DROP INDEX IF EXISTS devices_tenant_mac_key;

ALTER TABLE radius_access_reservations
    DROP CONSTRAINT IF EXISTS radius_access_reservations_subscription_id_normalized_mac_key;
ALTER TABLE portal_handoffs
    DROP CONSTRAINT IF EXISTS portal_handoffs_nonce_hash_key;
ALTER TABLE subscriptions
    DROP CONSTRAINT IF EXISTS subscriptions_voucher_id_key;
ALTER TABLE usage_counters
    DROP CONSTRAINT IF EXISTS usage_counters_subscription_id_period_start_key;
ALTER TABLE vouchers
    DROP CONSTRAINT IF EXISTS vouchers_tenant_id_code_hash_key;

COMMIT;
