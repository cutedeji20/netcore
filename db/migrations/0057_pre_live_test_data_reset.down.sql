BEGIN;
REVOKE EXECUTE ON FUNCTION tenant_test_data_reset_preview(uuid) FROM netcore_app_rw;
REVOKE EXECUTE ON FUNCTION tenant_test_data_reset_execute(uuid, uuid, text, text) FROM netcore_app_rw;
DROP FUNCTION IF EXISTS tenant_test_data_reset_execute(uuid, uuid, text, text);
DROP FUNCTION IF EXISTS tenant_test_data_reset_preview(uuid);
DROP TABLE IF EXISTS tenant_test_data_resets;
CREATE OR REPLACE FUNCTION audit_logs_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only (BUILD.md §39): % denied', TG_OP
        USING ERRCODE = '42501';
END;
$$;
DELETE FROM role_permissions WHERE permission_id = (SELECT id FROM permissions WHERE name = 'tenant.test_data_reset');
DELETE FROM permissions WHERE name = 'tenant.test_data_reset';
COMMIT;
