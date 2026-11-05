-- The recovered production database recorded historical migrations while
-- omitting several unique constraints and indexes. Voucher redemption relies
-- on these database-enforced invariants for single-use codes, single quota
-- periods, single-use portal handoffs, and bounded RADIUS reservations.
BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM vouchers
        GROUP BY tenant_id, code_hash
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore vouchers_tenant_id_code_hash_key: duplicate voucher hashes exist';
    END IF;

    IF EXISTS (
        SELECT 1 FROM usage_counters
        GROUP BY subscription_id, period_start
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore usage_counters_subscription_id_period_start_key: duplicate quota periods exist';
    END IF;

    IF EXISTS (
        SELECT 1 FROM subscriptions
        WHERE voucher_id IS NOT NULL
        GROUP BY voucher_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore subscriptions_voucher_id_key: vouchers fund multiple subscriptions';
    END IF;

    IF EXISTS (
        SELECT 1 FROM portal_handoffs
        GROUP BY nonce_hash
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore portal_handoffs_nonce_hash_key: duplicate handoff nonces exist';
    END IF;

    IF EXISTS (
        SELECT 1 FROM radius_access_reservations
        GROUP BY subscription_id, normalized_mac
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore radius_access_reservations_subscription_id_normalized_mac_key: duplicate access reservations exist';
    END IF;
END;
$$;

-- Fresh installations already have these constraints from their originating
-- migrations; a recovered installation does not. Attach an existing unique
-- index where present, otherwise create the constraint and its backing index.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'vouchers_tenant_id_code_hash_key') THEN
        IF to_regclass('public.vouchers_tenant_id_code_hash_key') IS NULL THEN
            ALTER TABLE vouchers ADD CONSTRAINT vouchers_tenant_id_code_hash_key UNIQUE (tenant_id, code_hash);
        ELSE
            ALTER TABLE vouchers ADD CONSTRAINT vouchers_tenant_id_code_hash_key UNIQUE USING INDEX vouchers_tenant_id_code_hash_key;
        END IF;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'usage_counters_subscription_id_period_start_key') THEN
        IF to_regclass('public.usage_counters_subscription_id_period_start_key') IS NULL THEN
            ALTER TABLE usage_counters ADD CONSTRAINT usage_counters_subscription_id_period_start_key UNIQUE (subscription_id, period_start);
        ELSE
            ALTER TABLE usage_counters ADD CONSTRAINT usage_counters_subscription_id_period_start_key UNIQUE USING INDEX usage_counters_subscription_id_period_start_key;
        END IF;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subscriptions_voucher_id_key') THEN
        IF to_regclass('public.subscriptions_voucher_id_key') IS NULL THEN
            ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_voucher_id_key UNIQUE (voucher_id);
        ELSE
            ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_voucher_id_key UNIQUE USING INDEX subscriptions_voucher_id_key;
        END IF;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'portal_handoffs_nonce_hash_key') THEN
        IF to_regclass('public.portal_handoffs_nonce_hash_key') IS NULL THEN
            ALTER TABLE portal_handoffs ADD CONSTRAINT portal_handoffs_nonce_hash_key UNIQUE (nonce_hash);
        ELSE
            ALTER TABLE portal_handoffs ADD CONSTRAINT portal_handoffs_nonce_hash_key UNIQUE USING INDEX portal_handoffs_nonce_hash_key;
        END IF;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'radius_access_reservations_subscription_id_normalized_mac_key') THEN
        IF to_regclass('public.radius_access_reservations_subscription_id_normalized_mac_key') IS NULL THEN
            ALTER TABLE radius_access_reservations ADD CONSTRAINT radius_access_reservations_subscription_id_normalized_mac_key UNIQUE (subscription_id, normalized_mac);
        ELSE
            ALTER TABLE radius_access_reservations ADD CONSTRAINT radius_access_reservations_subscription_id_normalized_mac_key UNIQUE USING INDEX radius_access_reservations_subscription_id_normalized_mac_key;
        END IF;
    END IF;
END;
$$;

CREATE UNIQUE INDEX IF NOT EXISTS devices_tenant_mac_key
    ON devices (tenant_id, normalized_mac)
    WHERE normalized_mac IS NOT NULL AND status <> 'REMOVED';

CREATE UNIQUE INDEX IF NOT EXISTS vouchers_order_position_unique
    ON vouchers (tenant_id, order_id, order_position)
    WHERE order_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS vouchers_tenant_batch_idx
    ON vouchers (tenant_id, batch_id);

COMMIT;
