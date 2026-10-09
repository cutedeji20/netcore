BEGIN;

ALTER TABLE customers
    ADD COLUMN is_guest boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT customers_guest_without_user CHECK (NOT is_guest OR user_id IS NULL);

ALTER TABLE subscriptions
    ADD COLUMN voucher_id uuid UNIQUE REFERENCES vouchers(id) ON DELETE RESTRICT,
    ADD CONSTRAINT subscriptions_voucher_funding CHECK (voucher_id IS NULL OR payment_status = 'PAID');

ALTER TABLE vouchers DROP CONSTRAINT vouchers_redeemed_complete;
ALTER TABLE vouchers ADD CONSTRAINT vouchers_redeemed_complete CHECK (
    (status = 'REDEEMED' AND redeemed_at IS NOT NULL)
    OR status = 'REVOKED'
    OR (status IN ('UNUSED','EXPIRED') AND redeemed_at IS NULL)
);

ALTER TABLE portal_handoffs ALTER COLUMN user_id DROP NOT NULL;
CREATE FUNCTION portal_guest_handoff_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
    IF NEW.user_id IS NULL AND NOT EXISTS (
        SELECT 1 FROM subscriptions s
        JOIN customers c ON c.id=s.customer_id AND c.tenant_id=s.tenant_id
        WHERE s.id=NEW.subscription_id AND s.tenant_id=NEW.tenant_id
          AND s.voucher_id IS NOT NULL AND c.is_guest AND c.user_id IS NULL
          AND s.status='ACTIVE'
    ) THEN
        RAISE EXCEPTION 'guest handoff requires active voucher-funded guest subscription' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER portal_guest_handoff_guard_trigger
    BEFORE INSERT OR UPDATE ON portal_handoffs
    FOR EACH ROW EXECUTE FUNCTION portal_guest_handoff_guard();

COMMIT;
