-- A deliberately guarded, tenant-scoped pre-live reset. This is not TRUNCATE:
-- TRUNCATE is cross-tenant and would bypass the database's audit guarantees.
BEGIN;

CREATE TABLE tenant_test_data_resets (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    actor_id         uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    backup_reference text NOT NULL CHECK (length(btrim(backup_reference)) BETWEEN 1 AND 300),
    reason           text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 240),
    removed          jsonb NOT NULL,
    completed_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tenant_test_data_resets_tenant_completed_idx
    ON tenant_test_data_resets (tenant_id, completed_at DESC);

ALTER TABLE tenant_test_data_resets ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_test_data_resets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_test_data_resets
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

-- Audit history remains immutable. Subscription-event rows are the one
-- dependent record that must be removed before a test subscription can be
-- removed. Only the SECURITY DEFINER reset function can set this transaction-
-- local flag; netcore_app_rw still has no DELETE permission on this table.
CREATE OR REPLACE FUNCTION audit_logs_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'subscription_events'
       AND TG_OP = 'DELETE'
       AND current_setting('netcore.test_data_reset', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'audit_logs is append-only (BUILD.md §39): % denied', TG_OP
        USING ERRCODE = '42501';
END;
$$;

CREATE FUNCTION tenant_test_data_reset_preview(p_tenant uuid)
RETURNS jsonb
LANGUAGE sql SECURITY DEFINER STABLE
SET search_path = public, pg_temp
AS $$
    SELECT CASE WHEN p_tenant = current_tenant_id() THEN jsonb_build_object(
        'customers',          (SELECT count(*) FROM customers WHERE tenant_id = p_tenant),
        'customer_users',     (SELECT count(DISTINCT c.user_id) FROM customers c WHERE c.tenant_id = p_tenant AND c.user_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = c.user_id)),
        'devices',            (SELECT count(*) FROM devices WHERE tenant_id = p_tenant),
        'subscriptions',      (SELECT count(*) FROM subscriptions WHERE tenant_id = p_tenant),
        'subscription_events',(SELECT count(*) FROM subscription_events WHERE tenant_id = p_tenant),
        'sessions',           (SELECT count(*) FROM sessions WHERE tenant_id = p_tenant),
        'accounting_records', (SELECT count(*) FROM accounting_records WHERE tenant_id = p_tenant),
        'payments',           (SELECT count(*) FROM payments WHERE tenant_id = p_tenant),
        'invoices',           (SELECT count(*) FROM invoices WHERE tenant_id = p_tenant),
        'vouchers',           (SELECT count(*) FROM vouchers WHERE tenant_id = p_tenant),
        'ledger_entries',     (SELECT count(*) FROM ledger_entries WHERE tenant_id = p_tenant),
        'outbox_events',      (SELECT count(*) FROM outbox_events WHERE tenant_id = p_tenant),
        'active_sessions',    (SELECT count(*) FROM sessions WHERE tenant_id = p_tenant AND status <> 'CLOSED'),
        'active_reservations',(SELECT count(*) FROM radius_access_reservations WHERE tenant_id = p_tenant AND expires_at > now()),
        'unpublished_outbox', (SELECT count(*) FROM outbox_events WHERE tenant_id = p_tenant AND published_at IS NULL)
    ) ELSE '{}'::jsonb END;
$$;

CREATE FUNCTION tenant_test_data_reset_execute(
    p_tenant uuid, p_actor uuid, p_backup_reference text, p_reason text
) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    v_removed jsonb;
    v_run_id uuid;
    v_completed_at timestamptz := now();
BEGIN
    IF p_tenant IS NULL OR p_actor IS NULL OR p_tenant <> current_tenant_id()
       OR length(btrim(COALESCE(p_backup_reference, ''))) NOT BETWEEN 1 AND 300
       OR length(btrim(COALESCE(p_reason, ''))) NOT BETWEEN 1 AND 240 THEN
        RAISE EXCEPTION 'invalid tenant test-data reset request' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM users u
        JOIN user_roles ur ON ur.user_id = u.id
        JOIN roles r ON r.id = ur.role_id AND r.tenant_id = u.tenant_id
        JOIN role_permissions rp ON rp.role_id = r.id
        JOIN permissions permission ON permission.id = rp.permission_id
        WHERE u.id = p_actor AND u.tenant_id = p_tenant AND u.status = 'ACTIVE'
          AND permission.name = 'tenant.test_data_reset'
    ) THEN
        RAISE EXCEPTION 'test-data reset permission denied' USING ERRCODE = '42501';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(p_tenant::text, 0));
    IF EXISTS (SELECT 1 FROM sessions WHERE tenant_id = p_tenant AND status <> 'CLOSED')
       OR EXISTS (SELECT 1 FROM radius_access_reservations WHERE tenant_id = p_tenant AND expires_at > now())
       OR EXISTS (SELECT 1 FROM outbox_events WHERE tenant_id = p_tenant AND published_at IS NULL) THEN
        RAISE EXCEPTION 'test-data reset is blocked by active sessions, reservations, or unpublished events' USING ERRCODE = 'P0001';
    END IF;

    SELECT tenant_test_data_reset_preview(p_tenant) INTO v_removed;
    PERFORM set_config('netcore.test_data_reset', 'on', true);
    CREATE TEMP TABLE reset_customer_users ON COMMIT DROP AS
      SELECT DISTINCT c.user_id
        FROM customers c
       WHERE c.tenant_id = p_tenant AND c.user_id IS NOT NULL
         AND NOT EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = c.user_id);

    DELETE FROM device_replacements WHERE tenant_id = p_tenant;
    DELETE FROM portal_handoffs WHERE tenant_id = p_tenant;
    DELETE FROM radius_access_reservations WHERE tenant_id = p_tenant;
    DELETE FROM accounting_records WHERE tenant_id = p_tenant;
    DELETE FROM nas_accounting_events WHERE tenant_id = p_tenant;
    DELETE FROM usage_counters WHERE tenant_id = p_tenant;
    DELETE FROM sessions WHERE tenant_id = p_tenant;
    DELETE FROM payments WHERE tenant_id = p_tenant;
    DELETE FROM invoices WHERE tenant_id = p_tenant;
    DELETE FROM vouchers WHERE tenant_id = p_tenant;
    DELETE FROM idempotency_keys WHERE tenant_id = p_tenant;
    DELETE FROM outbox_events WHERE tenant_id = p_tenant;
    DELETE FROM ledger_entries WHERE tenant_id = p_tenant;
    DELETE FROM ledger_transactions WHERE tenant_id = p_tenant;
    DELETE FROM ledger_accounts WHERE tenant_id = p_tenant;
    DELETE FROM subscription_events WHERE tenant_id = p_tenant;
    DELETE FROM subscriptions WHERE tenant_id = p_tenant;
    DELETE FROM devices WHERE tenant_id = p_tenant;
    DELETE FROM auth_sessions WHERE tenant_id = p_tenant AND user_id IN (SELECT user_id FROM reset_customer_users);
    DELETE FROM user_mfa_totp WHERE tenant_id = p_tenant AND user_id IN (SELECT user_id FROM reset_customer_users);
    DELETE FROM customers WHERE tenant_id = p_tenant;
    DELETE FROM users WHERE tenant_id = p_tenant AND id IN (SELECT user_id FROM reset_customer_users);

    INSERT INTO tenant_test_data_resets (tenant_id, actor_id, backup_reference, reason, removed, completed_at)
    VALUES (p_tenant, p_actor, btrim(p_backup_reference), btrim(p_reason), v_removed, v_completed_at)
    RETURNING id INTO v_run_id;
    INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, metadata)
    VALUES (p_tenant, 'USER', p_actor, 'TEST_DATA_RESET_COMPLETED', 'tenant', p_tenant,
            jsonb_build_object('reset_run_id', v_run_id, 'backup_reference', btrim(p_backup_reference), 'removed', v_removed));
    RETURN v_removed || jsonb_build_object('run_id', v_run_id, 'completed_at', v_completed_at);
END;
$$;

REVOKE ALL ON FUNCTION tenant_test_data_reset_preview(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION tenant_test_data_reset_execute(uuid, uuid, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION tenant_test_data_reset_preview(uuid) TO netcore_app_rw;
GRANT EXECUTE ON FUNCTION tenant_test_data_reset_execute(uuid, uuid, text, text) TO netcore_app_rw;

INSERT INTO permissions (name) VALUES ('tenant.test_data_reset') ON CONFLICT (name) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT role.id, permission.id
  FROM roles role CROSS JOIN permissions permission
 WHERE role.name = 'Administrator' AND permission.name = 'tenant.test_data_reset'
ON CONFLICT DO NOTHING;

COMMIT;
