-- Voucher claims for enrolled devices may be paid now but must not replace an
-- active entitlement. Pending subscriptions carry that durable queue state;
-- this index makes per-device scheduling and worker activation bounded.
BEGIN;

CREATE INDEX subscriptions_device_voucher_schedule_idx
    ON subscriptions (tenant_id, device_id, expires_at)
    WHERE status IN ('ACTIVE', 'PENDING') AND voucher_id IS NOT NULL;

COMMENT ON INDEX subscriptions_device_voucher_schedule_idx IS
    'Supports serialized existing-device voucher claims and due activation.';

COMMIT;
