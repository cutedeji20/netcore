BEGIN;
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['customers','subscriptions','payments','invoices','sessions',
    'usage_counters','ledger_entries','devices','vouchers','plans',
    'subscription_events','outbox_events'] LOOP
    IF EXISTS (SELECT 1 FROM pg_tables WHERE tablename = t) THEN
      EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
      EXECUTE format('ALTER TABLE %I DISABLE ROW LEVEL SECURITY', t);
    END IF;
  END LOOP;
END$$;
DROP FUNCTION IF EXISTS radius_quota_lookup(uuid, timestamptz);
DROP FUNCTION IF EXISTS current_tenant_id();
COMMIT;
