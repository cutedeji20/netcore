-- 0004_roles_rls.up.sql
-- Spec: BUILD.md §10 (tenant isolation + RLS), §60.1 (exact radius grants)
--
-- v1.2 CORRECTION — read this before touching RLS on usage_counters.
--
-- v1.1 §10 put usage_counters under RLS, while §21A.4/§60.1 have FreeRADIUS
-- read it directly via rlm_sql. Those are incompatible: rlm_sql has no place
-- to SET LOCAL app.tenant_id, and it cannot know the tenant before the very
-- lookup that determines it. With RLS on and the GUC unset the predicate is
-- NULL, the row is filtered, and §82's "quota lookup fails" row fires —
-- EVERY SESSION ON THE PLATFORM SILENTLY GETS A 256 MiB BUDGET.
--
-- That is a fail-safe outcome for the wrong reason, and it would be
-- misdiagnosed as a capacity problem for weeks.
--
-- Fix: FreeRADIUS never touches the table. It calls a SECURITY DEFINER
-- function that takes the subscription id, resolves the tenant itself, and
-- returns only quota figures. RLS stays on for every application path.

BEGIN;

-- ---------------------------------------------------------------------------
-- §60 database roles. NOLOGIN here; passwords/auth are environment-provided.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'netcore_app_rw')   THEN CREATE ROLE netcore_app_rw   NOLOGIN; END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'netcore_app_ro')   THEN CREATE ROLE netcore_app_ro   NOLOGIN; END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'netcore_radius')   THEN CREATE ROLE netcore_radius   NOLOGIN; END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'netcore_migration')THEN CREATE ROLE netcore_migration NOLOGIN; END IF;
END$$;

GRANT USAGE ON SCHEMA public TO netcore_app_rw, netcore_app_ro, netcore_radius;

-- app_rw: full DML, no DDL.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO netcore_app_rw;

-- §39/§60: append-only tables. Revoke after the blanket grant above.
REVOKE UPDATE, DELETE ON audit_logs, subscription_events, ledger_entries,
                          accounting_records FROM netcore_app_rw;

-- app_ro: reporting.
GRANT SELECT ON ALL TABLES IN SCHEMA public TO netcore_app_ro;

-- ---------------------------------------------------------------------------
-- §21A.4 + §60.1 — the FreeRADIUS quota read, RLS-safe.
-- SECURITY DEFINER: runs as the function owner (migration role), so it is not
-- subject to the caller's RLS policies. It returns quota figures ONLY — no
-- customer data, no tenant data, nothing worth stealing.
-- ---------------------------------------------------------------------------
CREATE FUNCTION radius_quota_lookup(
    p_subscription_id uuid,
    p_at              timestamptz DEFAULT now()
) RETURNS TABLE (quota_bytes bigint, consumed_bytes bigint, exhausted boolean)
LANGUAGE sql SECURITY DEFINER STABLE
SET search_path = public, pg_temp
AS $$
    SELECT uc.quota_bytes,
           uc.consumed_bytes,
           (uc.exhausted_at IS NOT NULL OR uc.consumed_bytes >= uc.quota_bytes)
      FROM usage_counters uc
     WHERE uc.subscription_id = p_subscription_id
       AND uc.period_start <= p_at
       AND uc.period_end   >  p_at
     LIMIT 1;
$$;

REVOKE ALL ON FUNCTION radius_quota_lookup(uuid, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION radius_quota_lookup(uuid, timestamptz) TO netcore_radius;

COMMENT ON FUNCTION radius_quota_lookup IS
'BUILD.md §21A.4 + v1.2 RLS correction. SECURITY DEFINER so FreeRADIUS can read '
'quota without a tenant GUC. Returns quota figures only. netcore_radius has NO '
'direct SELECT on usage_counters.';

-- ---------------------------------------------------------------------------
-- §60.1 — exact netcore_radius grants. Nothing broader.
-- ---------------------------------------------------------------------------
GRANT SELECT ON nas TO netcore_radius;   -- §22: which NAS may speak to us at all
GRANT SELECT ON sessions TO netcore_radius;

-- §24A.4: Simultaneous-Use must be able to reap a stale session INLINE, or a
-- zombie locks a paying customer out. Column-scoped so FreeRADIUS can close a
-- session but cannot reassign its owner.
GRANT UPDATE (status, ended_at, close_reason, terminate_cause, last_interim_at)
    ON sessions TO netcore_radius;

GRANT INSERT ON accounting_records TO netcore_radius;
GRANT INSERT ON nas_accounting_events TO netcore_radius;
GRANT EXECUTE ON FUNCTION quota_apply(uuid, text, timestamptz, bigint) TO netcore_radius;

-- Explicitly NOT granted (asserted by tests/security/db_grants_test):
--   payments, invoices, ledger_*, users, customers, audit_logs,
--   usage_counters (direct), any *_ref column, any DDL.

-- ---------------------------------------------------------------------------
-- §10 Row-Level Security — defence in depth behind the application predicate.
-- The application ALSO writes `AND tenant_id = $n`. RLS turns a forgotten
-- predicate into zero rows instead of another tenant's data.
-- ---------------------------------------------------------------------------
CREATE FUNCTION current_tenant_id() RETURNS uuid
LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid;
$$;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'customers','subscriptions','payments','invoices','sessions',
        'usage_counters','ledger_entries','devices','vouchers','plans',
        'subscription_events','outbox_events'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($f$
            CREATE POLICY tenant_isolation ON %I
                USING (tenant_id = current_tenant_id())
                WITH CHECK (tenant_id = current_tenant_id())
        $f$, t);
    END LOOP;
END$$;

-- The migration role owns the tables and is exempt (needed for maintenance).
-- netcore_app_rw is NOT exempt: that is the entire point.

COMMIT;
