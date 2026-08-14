-- 0027_payment_activation_guards.up.sql
-- Payment initiation is idempotent per customer request and successful
-- payments are immutable financial facts.  Activation itself is performed by
-- the application in one tenant-scoped transaction after gateway verification.

BEGIN;

-- idempotency_keys has always carried tenant_id.  It must have the same
-- forced boundary as the payment it protects, otherwise one forgotten
-- predicate becomes a cross-tenant replay oracle.
ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotency_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON idempotency_keys
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

-- A refund is a new forward record.  Once a payment has become SUCCESS no
-- later request may alter or delete the fact that was verified.
CREATE FUNCTION payments_immutable_after_success() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'SUCCESS' THEN
        RAISE EXCEPTION 'payments with status SUCCESS are immutable (BUILD.md §89): % denied', TG_OP
            USING ERRCODE = '42501';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER payments_no_change_after_success
    BEFORE UPDATE OR DELETE ON payments
    FOR EACH ROW EXECUTE FUNCTION payments_immutable_after_success();

COMMIT;
