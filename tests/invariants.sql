-- tests/invariants.sql
-- Executable proof of every database-enforced invariant in BUILD.md §8.
-- Run: make test-db     (CI gate, §70)
--
-- Each test prints PASS or FAIL. Any FAIL fails the build.

\set ON_ERROR_STOP on
\pset pager off
\set QUIET on
SET client_min_messages = warning;

CREATE TEMP TABLE results (n int GENERATED ALWAYS AS IDENTITY, name text, verdict text);
CREATE OR REPLACE FUNCTION pg_temp.check(nm text, ok boolean, detail text DEFAULT '')
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO results (name, verdict)
    VALUES (nm, CASE WHEN ok THEN 'PASS' ELSE 'FAIL  <-- ' || detail END);
END$$;

-- ===========================================================================
-- Fixtures
-- ===========================================================================
INSERT INTO tenants (id, name, slug, currency, timezone)
VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid, 'Test ISP', 'test', 'NGN', 'Africa/Lagos');

INSERT INTO users (id, tenant_id, email, password_hash)
VALUES ('00000000-0000-0000-0000-0000000000f1'::uuid,
        '00000000-0000-0000-0000-0000000000a1'::uuid,
        'admin@test.invalid', '$argon2id$v=19$m=65536,t=3,p=2$fixture$fixture');

INSERT INTO customers (id, tenant_id, customer_number)
VALUES ('00000000-0000-0000-0000-0000000000c1'::uuid,
        '00000000-0000-0000-0000-0000000000a1'::uuid, 'CUST-001');

UPDATE customers
   SET user_id = '00000000-0000-0000-0000-0000000000f1'::uuid
 WHERE id = '00000000-0000-0000-0000-0000000000c1'::uuid;

INSERT INTO plans (id, tenant_id, name, price_minor, currency, duration_seconds,
                   download_bps, upload_bps, quota_bytes, quota_reset_policy,
                   quota_exhausted_action, throttle_download_bps, throttle_upload_bps)
VALUES ('00000000-0000-0000-0000-0000000000b1'::uuid,
        '00000000-0000-0000-0000-0000000000a1'::uuid,
        '10GB Monthly', 500000, 'NGN', 2592000, 20000000, 10000000,
        10737418240, 'MONTHLY', 'THROTTLE', 512000, 256000);

INSERT INTO subscriptions (id, tenant_id, customer_id, plan_id, status, starts_at, expires_at)
VALUES ('00000000-0000-0000-0000-0000000000d1'::uuid,
        '00000000-0000-0000-0000-0000000000a1'::uuid,
        '00000000-0000-0000-0000-0000000000c1'::uuid,
        '00000000-0000-0000-0000-0000000000b1'::uuid,
        'ACTIVE', '2026-08-01+00', '2026-09-01+00');

INSERT INTO usage_counters (tenant_id, subscription_id, customer_id,
                            period_start, period_end, quota_bytes)
VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
        '00000000-0000-0000-0000-0000000000d1'::uuid,
        '00000000-0000-0000-0000-0000000000c1'::uuid,
        '2026-08-01+00', '2026-09-01+00', 10737418240);

INSERT INTO routers (id, tenant_id, name, management_ip, credential_ref, radius_secret_ref)
VALUES ('00000000-0000-0000-0000-0000000000e1'::uuid,
        '00000000-0000-0000-0000-0000000000a1'::uuid,
        'rb5009-site-a', '10.10.0.1',
        'netcore/routers/r1/api', 'netcore/routers/r1/radius');

INSERT INTO nas (id, tenant_id, router_id, nasname, shortname, secret_ref)
VALUES ('00000000-0000-0000-0000-0000000000e2'::uuid,
        '00000000-0000-0000-0000-0000000000a1'::uuid,
        '00000000-0000-0000-0000-0000000000e1'::uuid,
        '10.10.0.1', 'site-a-hotspot', 'netcore/routers/r1/radius');

-- ===========================================================================
-- §21A.8  QUOTA IDEMPOTENCY — the money invariant
-- ===========================================================================
DO $$
DECLARE
    sub uuid := '00000000-0000-0000-0000-0000000000d1';
    t   timestamptz := '2026-08-11 10:00:00+00';
    c   bigint;
BEGIN
    PERFORM quota_apply(sub, 'sessA', t, 1073741824);              -- 1 GiB
    PERFORM quota_apply(sub, 'sessA', t + interval '5m', 2147483648); -- 2 GiB cumulative
    PERFORM quota_apply(sub, 'sessA', t + interval '5m', 2147483648); -- RETRANSMIT
    PERFORM quota_apply(sub, 'sessA', t + interval '6m', 1073741824); -- OUT OF ORDER
    PERFORM quota_apply(sub, 'sessB', t + interval '7m', 524288000);  -- 2nd device

    SELECT consumed_bytes INTO c FROM usage_counters WHERE subscription_id = sub;
    PERFORM pg_temp.check('§21A.8 replay + out-of-order + multi-session pooling',
        c = 2147483648 + 524288000,
        format('expected %s got %s', 2147483648 + 524288000, c));
END$$;

-- Exhaustion is latched exactly once
DO $$
DECLARE sub uuid := '00000000-0000-0000-0000-0000000000d1'; e1 timestamptz; e2 timestamptz;
BEGIN
    PERFORM quota_apply(sub, 'sessC', '2026-08-11 11:00:00+00', 10737418240);
    SELECT exhausted_at INTO e1 FROM usage_counters WHERE subscription_id = sub;
    PERFORM quota_apply(sub, 'sessC', '2026-08-11 11:05:00+00', 11737418240);
    SELECT exhausted_at INTO e2 FROM usage_counters WHERE subscription_id = sub;
    PERFORM pg_temp.check('§21A.3 exhausted_at latches once, not on every packet',
        e1 IS NOT NULL AND e1 = e2, 'exhausted_at moved on a later packet');
END$$;

-- A packet whose period has no counter row must RAISE, not silently vanish
DO $$
DECLARE ok boolean := false;
BEGIN
    BEGIN
        PERFORM quota_apply('00000000-0000-0000-0000-0000000000d1'::uuid,
                            'sessX', '2030-01-01+00', 5000);
    EXCEPTION WHEN OTHERS THEN ok := true;
    END;
    PERFORM pg_temp.check('v1.2 missing counter row raises instead of discarding revenue',
        ok, 'quota_apply silently ignored traffic with no counter row');
END$$;

-- Negative cumulative is rejected
DO $$
DECLARE ok boolean := false;
BEGIN
    BEGIN
        PERFORM quota_apply('00000000-0000-0000-0000-0000000000d1'::uuid,
                            'sessN', '2026-08-11 12:00:00+00', -5);
    EXCEPTION WHEN OTHERS THEN ok := true;
    END;
    PERFORM pg_temp.check('quota_apply rejects negative cumulative', ok, '');
END$$;

-- ===========================================================================
-- §21A.6  GIGAWORDS
-- ===========================================================================
DO $$
BEGIN
    PERFORM pg_temp.check('§21A.6 gigawords arithmetic (gw=12, oct=100)',
        ((12::bigint << 32) | 100::bigint) = 51539607652,
        'high-word reassembly wrong');
END$$;

-- ===========================================================================
-- §21A.7 + v1.2  RESET CARRY-FORWARD
-- A session open across the boundary must NOT be re-billed from zero.
-- ===========================================================================
DO $$
DECLARE
    sub uuid := '00000000-0000-0000-0000-0000000000d1';
    newid uuid; c bigint; wm jsonb;
BEGIN
    -- sessD is still open at the boundary
    INSERT INTO sessions (tenant_id, customer_id, subscription_id, router_id,
                          acct_session_id, acct_unique_id, nas_ip_address, status)
    VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
            '00000000-0000-0000-0000-0000000000c1'::uuid, sub,
            '00000000-0000-0000-0000-0000000000e1'::uuid,
            '0x0d1', 'sessD', '10.10.0.1', 'ACTIVE');

    PERFORM quota_apply(sub, 'sessD', '2026-08-31 23:55:00+00', 3221225472); -- 3 GiB

    newid := quota_reset(sub, '2026-09-01+00', '2026-10-01+00', 10737418240);

    SELECT last_applied_watermark INTO wm FROM usage_counters WHERE id = newid;
    PERFORM pg_temp.check('v1.2 open session watermark carried across reset',
        (wm ->> 'sessD')::bigint = 3221225472,
        'watermark map: ' || COALESCE(wm::text,'NULL'));

    -- First packet in the new period reports 3.5 GiB cumulative.
    -- Only the 0.5 GiB delta may be billed, not the whole 3.5 GiB.
    PERFORM quota_apply(sub, 'sessD', '2026-09-01 00:05:00+00', 3758096384);
    SELECT consumed_bytes INTO c FROM usage_counters WHERE id = newid;
    PERFORM pg_temp.check('v1.2 boundary-spanning session bills only the delta',
        c = 3758096384 - 3221225472,
        format('expected %s got %s', 3758096384 - 3221225472, c));

    -- Closed sessions must NOT be carried (keeps the jsonb map bounded)
    PERFORM pg_temp.check('v1.2 closed-session watermarks pruned at reset',
        NOT (wm ? 'sessA'), 'closed session leaked into new period');
END$$;

-- ===========================================================================
-- §9.1  ACCOUNTING DEDUP KEY
-- ===========================================================================
DO $$
DECLARE n int;
BEGIN
    INSERT INTO accounting_records (tenant_id, router_id, acct_session_id, acct_unique_id,
        acct_status_type, event_timestamp, acct_session_time, input_octets)
    VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
            '00000000-0000-0000-0000-0000000000e1'::uuid,
            '0x0a1','uniqA','INTERIM','2026-08-11 10:00:00+00',300,1073741824)
    ON CONFLICT ON CONSTRAINT accounting_dedup_key DO NOTHING;

    -- exact retransmit, arriving later (different created_at)
    PERFORM pg_sleep(0.02);
    INSERT INTO accounting_records (tenant_id, router_id, acct_session_id, acct_unique_id,
        acct_status_type, event_timestamp, acct_session_time, input_octets)
    VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
            '00000000-0000-0000-0000-0000000000e1'::uuid,
            '0x0a1','uniqA','INTERIM','2026-08-11 10:00:00+00',300,1073741824)
    ON CONFLICT ON CONSTRAINT accounting_dedup_key DO NOTHING;

    -- genuine next interim
    INSERT INTO accounting_records (tenant_id, router_id, acct_session_id, acct_unique_id,
        acct_status_type, event_timestamp, acct_session_time, input_octets)
    VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
            '00000000-0000-0000-0000-0000000000e1'::uuid,
            '0x0a1','uniqA','INTERIM','2026-08-11 10:05:00+00',600,2147483648)
    ON CONFLICT ON CONSTRAINT accounting_dedup_key DO NOTHING;

    SELECT count(*) INTO n FROM accounting_records WHERE acct_unique_id = 'uniqA';
    PERFORM pg_temp.check('§9.1 retransmit deduped, genuine update admitted',
        n = 2, format('expected 2 rows, got %s', n));
END$$;

-- Clock-skewed packet lands in DEFAULT partition rather than being rejected
DO $$
DECLARE n int;
BEGIN
    INSERT INTO accounting_records (tenant_id, router_id, acct_session_id, acct_unique_id,
        acct_status_type, event_timestamp, acct_session_time, input_octets)
    VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
            '00000000-0000-0000-0000-0000000000e1'::uuid,
            '0x0z9','uniqSkew','START','2019-01-01 00:00:00+00',0,0);
    SELECT count(*) INTO n FROM accounting_records_default WHERE acct_unique_id='uniqSkew';
    PERFORM pg_temp.check('§9.1 clock-skewed packet retained via DEFAULT partition',
        n = 1, 'skewed accounting packet was lost');
END$$;

-- ===========================================================================
-- Phase 8  NARROW RADIUS ACCOUNTING INGESTION
-- The one function must atomically create/close sessions, preserve both
-- gigawords, deduplicate retransmits, and charge only cumulative growth.
-- ===========================================================================
DO $$
DECLARE
    before_consumed bigint;
    after_consumed bigint;
    records int;
    final_status text;
    final_reason text;
    final_duration bigint;
    t0 bigint := extract(epoch FROM '2026-08-12 12:00:00+00'::timestamptz)::bigint;
BEGIN
    SELECT consumed_bytes INTO before_consumed
      FROM usage_counters
     WHERE subscription_id = '00000000-0000-0000-0000-0000000000d1'::uuid
       AND period_start = '2026-08-01+00';

    PERFORM radius_accounting_ingest(
        '10.10.0.1', 'Start', 'rad-0001', 'radius-unique-0001',
        'netcore:00000000-0000-0000-0000-0000000000d1', t0,
        0, 0, 0, 0, 0, '10.20.0.8', 'aa:bb:cc:dd:ee:01', NULL
    );
    PERFORM radius_accounting_ingest(
        '10.10.0.1', 'Interim-Update', 'rad-0001', 'radius-unique-0001',
        'netcore:00000000-0000-0000-0000-0000000000d1', t0 + 5,
        5, 100, 1, 50, 0, '10.20.0.8', 'aa:bb:cc:dd:ee:01', NULL
    );
    -- Exact retransmit: both accounting record and quota delta are a no-op.
    PERFORM radius_accounting_ingest(
        '10.10.0.1', 'Interim-Update', 'rad-0001', 'radius-unique-0001',
        'netcore:00000000-0000-0000-0000-0000000000d1', t0 + 5,
        5, 100, 1, 50, 0, '10.20.0.8', 'aa:bb:cc:dd:ee:01', NULL
    );
    PERFORM radius_accounting_ingest(
        '10.10.0.1', 'Stop', 'rad-0001', 'radius-unique-0001',
        'netcore:00000000-0000-0000-0000-0000000000d1', t0 + 10,
        10, 120, 1, 80, 0, '10.20.0.8', 'aa:bb:cc:dd:ee:01', 'User-Request'
    );
    PERFORM radius_accounting_ingest(
        '10.10.0.1', 'Stop', 'rad-0001', 'radius-unique-0001',
        'netcore:00000000-0000-0000-0000-0000000000d1', t0 + 10,
        10, 120, 1, 80, 0, '10.20.0.8', 'aa:bb:cc:dd:ee:01', 'User-Request'
    );

    SELECT count(*) INTO records
      FROM accounting_records WHERE acct_unique_id = 'radius-unique-0001';
    SELECT status, close_reason, duration_seconds
      INTO final_status, final_reason, final_duration
      FROM sessions WHERE acct_unique_id = 'radius-unique-0001';
    SELECT consumed_bytes INTO after_consumed
      FROM usage_counters
     WHERE subscription_id = '00000000-0000-0000-0000-0000000000d1'::uuid
       AND period_start = '2026-08-01+00';

    PERFORM pg_temp.check('Phase 8 RADIUS accounting records Start/Interim/Stop once',
        records = 3, format('expected 3 records got %s', records));
    PERFORM pg_temp.check('Phase 8 RADIUS accounting closes the session on Stop',
        final_status = 'CLOSED' AND final_reason = 'STOP' AND final_duration = 10,
        format('status=%s reason=%s duration=%s', final_status, final_reason, final_duration));
    PERFORM pg_temp.check('Phase 8 RADIUS accounting reassembles gigawords and bills final growth once',
        after_consumed - before_consumed = 4294967496,
        format('expected delta 4294967496 got %s', after_consumed - before_consumed));
END$$;

DO $$
DECLARE
    events int;
    closed_status text;
    closed_reason text;
    t0 bigint := extract(epoch FROM '2026-08-12 13:00:00+00'::timestamptz)::bigint;
BEGIN
    INSERT INTO sessions (
        tenant_id, customer_id, subscription_id, router_id, acct_session_id,
        acct_unique_id, nas_ip_address, started_at, last_interim_at, status
    ) VALUES (
        '00000000-0000-0000-0000-0000000000a1'::uuid,
        '00000000-0000-0000-0000-0000000000c1'::uuid,
        '00000000-0000-0000-0000-0000000000d1'::uuid,
        '00000000-0000-0000-0000-0000000000e1'::uuid,
        'rad-on-off-0001', 'radius-on-off-0001', '10.10.0.1',
        '2026-08-12 12:00:00+00', '2026-08-12 12:59:00+00', 'ACTIVE'
    );

    PERFORM radius_accounting_ingest(
        '10.10.0.1', 'Accounting-On', '', '', '', t0,
        0, 0, 0, 0, 0, NULL, NULL, NULL
    );
    PERFORM radius_accounting_ingest(
        '10.10.0.1', 'Accounting-On', '', '', '', t0,
        0, 0, 0, 0, 0, NULL, NULL, NULL
    );

    SELECT count(*) INTO events
      FROM nas_accounting_events
     WHERE nas_ip_address = '10.10.0.1' AND acct_status_type = 'ON'
       AND event_timestamp = to_timestamp(t0);
    SELECT status, close_reason INTO closed_status, closed_reason
      FROM sessions WHERE acct_unique_id = 'radius-on-off-0001';
    PERFORM pg_temp.check('Phase 8 Accounting-On is deduplicated and closes prior sessions',
        events = 1 AND closed_status = 'CLOSED' AND closed_reason = 'ACCT_ON_OFF',
        format('events=%s status=%s reason=%s', events, closed_status, closed_reason));
END$$;

-- ===========================================================================
-- §24A  SESSION INTEGRITY
-- ===========================================================================
DO $$
DECLARE ok boolean := false;
BEGIN
    BEGIN
        INSERT INTO sessions (tenant_id, customer_id, subscription_id, router_id,
                              acct_session_id, acct_unique_id, nas_ip_address)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
                '00000000-0000-0000-0000-0000000000c1'::uuid,
                '00000000-0000-0000-0000-0000000000d1'::uuid,
                '00000000-0000-0000-0000-0000000000e1'::uuid,
                '0x0d1','sessD-dup','10.10.0.1');   -- same router+acct_session_id as sessD
    EXCEPTION WHEN unique_violation THEN ok := true;
    END;
    PERFORM pg_temp.check('§24A duplicate Accounting-Start cannot create a 2nd active session',
        ok, 'duplicate active session admitted');
END$$;

DO $$
DECLARE ok boolean := false;
BEGIN
    BEGIN
        UPDATE sessions SET status='CLOSED' WHERE acct_unique_id='sessD'; -- no close_reason
    EXCEPTION WHEN check_violation THEN ok := true;
    END;
    PERFORM pg_temp.check('§24A a CLOSED session must record HOW it closed', ok, '');
END$$;

-- ===========================================================================
-- §38  LEDGER MUST BALANCE
-- ===========================================================================
DO $$
DECLARE ok boolean := false; txn uuid; acc1 uuid; acc2 uuid;
BEGIN
    INSERT INTO ledger_accounts (tenant_id, owner_type, account_type, currency)
    VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,'CUSTOMER','ASSET','NGN')
    RETURNING id INTO acc1;
    INSERT INTO ledger_accounts (tenant_id, owner_type, account_type, currency)
    VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,'PLATFORM','REVENUE','NGN')
    RETURNING id INTO acc2;

    -- balanced: commits
    BEGIN
        INSERT INTO ledger_transactions (tenant_id, reference)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,'ok') RETURNING id INTO txn;
        INSERT INTO ledger_entries (tenant_id, transaction_id, account_id, direction, amount_minor, currency)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,txn,acc1,'DEBIT',500000,'NGN'),
               ('00000000-0000-0000-0000-0000000000a1'::uuid,txn,acc2,'CREDIT',500000,'NGN');
    END;

    -- Unbalanced: the trigger is DEFERRABLE INITIALLY DEFERRED, so it would
    -- normally fire at COMMIT — outside this block, where it cannot be caught.
    -- Force it IMMEDIATE so the subtransaction sees it.
    BEGIN
        INSERT INTO ledger_transactions (tenant_id, reference)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,'bad') RETURNING id INTO txn;
        INSERT INTO ledger_entries (tenant_id, transaction_id, account_id, direction, amount_minor, currency)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,txn,acc1,'DEBIT',500000,'NGN');
        SET CONSTRAINTS ledger_entries_balanced IMMEDIATE;
    EXCEPTION WHEN OTHERS THEN ok := true;
    END;
    SET CONSTRAINTS ALL DEFERRED;
    PERFORM pg_temp.check('§38 unbalanced ledger transaction is rejected by the DB',
        ok, 'an unbalanced transaction committed');
END$$;

-- ===========================================================================
-- §39  AUDIT APPEND-ONLY
-- ===========================================================================
DO $$
DECLARE u boolean := false; d boolean := false;
BEGIN
    INSERT INTO audit_logs (tenant_id, actor_type, action)
    VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,'ADMIN','login');
    BEGIN UPDATE audit_logs SET action='tampered'; EXCEPTION WHEN OTHERS THEN u := true; END;
    BEGIN DELETE FROM audit_logs;                  EXCEPTION WHEN OTHERS THEN d := true; END;
    PERFORM pg_temp.check('§39 audit_logs rejects UPDATE', u, '');
    PERFORM pg_temp.check('§39 audit_logs rejects DELETE', d, '');
END$$;

-- ===========================================================================
-- §18  PAYMENT INTEGRITY
-- ===========================================================================
-- ===========================================================================
-- Phase 2 browser-session token storage
-- ===========================================================================
DO $$
DECLARE bad_length boolean := false;
BEGIN
    BEGIN
        INSERT INTO auth_sessions (tenant_id, user_id, token_hash, expires_at)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
                '00000000-0000-0000-0000-0000000000f1'::uuid,
                decode(repeat('00', 31), 'hex'), now() + interval '1 hour');
    EXCEPTION WHEN check_violation THEN bad_length := true;
    END;
    PERFORM pg_temp.check('Phase 2 auth session stores a SHA-256-sized token digest only',
        bad_length, 'token digest shorter than SHA-256 was accepted');
END$$;

-- ===========================================================================
-- §13  CAPTIVE-PORTAL HANDOFFS
-- The RADIUS function must consume exactly once and require the active NAS +
-- client MAC bindings; a raw nonce is never stored.
-- ===========================================================================
DO $$
DECLARE
    first_use int;
    replay int;
    wrong_mac int;
    expired int;
    raw_nonce text := 'AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcde';
    expired_nonce text := repeat('Z', 43);
BEGIN
    -- The test token is 43 URL-safe characters, like a 32-byte base64url nonce.
    raw_nonce := substring(raw_nonce from 1 for 43);
    INSERT INTO portal_handoffs (
        tenant_id, subscription_id, nas_id, user_id, client_mac, nonce_hash, expires_at
    ) VALUES (
        '00000000-0000-0000-0000-0000000000a1'::uuid,
        '00000000-0000-0000-0000-0000000000d1'::uuid,
        '00000000-0000-0000-0000-0000000000e2'::uuid,
        '00000000-0000-0000-0000-0000000000f1'::uuid,
        'aabbccddeeff', digest(raw_nonce, 'sha256'), now() + interval '90 seconds'
    );
    SELECT count(*) INTO wrong_mac
      FROM radius_portal_handoff_consume(raw_nonce, '10.10.0.1', 'aa:bb:cc:dd:ee:00');
    SELECT count(*) INTO first_use
      FROM radius_portal_handoff_consume(raw_nonce, '10.10.0.1', 'aa:bb:cc:dd:ee:ff');
    SELECT count(*) INTO replay
      FROM radius_portal_handoff_consume(raw_nonce, '10.10.0.1', 'aa:bb:cc:dd:ee:ff');

    INSERT INTO portal_handoffs (
        tenant_id, subscription_id, nas_id, user_id, client_mac, nonce_hash, created_at, expires_at
    ) VALUES (
        '00000000-0000-0000-0000-0000000000a1'::uuid,
        '00000000-0000-0000-0000-0000000000d1'::uuid,
        '00000000-0000-0000-0000-0000000000e2'::uuid,
        '00000000-0000-0000-0000-0000000000f1'::uuid,
        'aabbccddeeff', digest(expired_nonce, 'sha256'),
        now() - interval '121 seconds', now() - interval '1 second'
    );
    SELECT count(*) INTO expired
      FROM radius_portal_handoff_consume(expired_nonce, '10.10.0.1', 'aa:bb:cc:dd:ee:ff');

    PERFORM pg_temp.check('§13 handoff rejects wrong MAC, accepts once, then rejects replay',
        wrong_mac = 0 AND first_use = 1 AND replay = 0,
        format('wrong=%s first=%s replay=%s', wrong_mac, first_use, replay));
    PERFORM pg_temp.check('§13 expired handoff is rejected',
        expired = 0, format('expired result=%s', expired));
END$$;

-- ===========================================================================
-- Phase 2 MFA metadata: secret references only, tenant-scoped and replay-safe
-- ===========================================================================
DO $$
DECLARE bad_ref boolean := false; bad_status boolean := false;
BEGIN
    BEGIN
        INSERT INTO user_mfa_totp (tenant_id, user_id, secret_ref)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
                '00000000-0000-0000-0000-0000000000f1'::uuid,
                'JBSWY3DPEHPK3PXP copied secret value');
    EXCEPTION WHEN check_violation THEN bad_ref := true;
    END;
    BEGIN
        INSERT INTO user_mfa_totp (tenant_id, user_id, secret_ref, status)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
                '00000000-0000-0000-0000-0000000000f1'::uuid,
                'netcore/mfa/test/admin', 'ACTIVE');
    EXCEPTION WHEN check_violation THEN bad_status := true;
    END;
    PERFORM pg_temp.check('Phase 2 MFA rejects a copied secret in secret_ref',
        bad_ref, 'TOTP secret value accepted in PostgreSQL');
    PERFORM pg_temp.check('Phase 2 MFA ACTIVE device requires an enabled timestamp',
        bad_status, 'active MFA device was created without verification');
END$$;

-- Payment integrity
DO $$
DECLARE ok boolean := false; dup boolean := false; immutable boolean := false;
BEGIN
    BEGIN
        INSERT INTO payments (tenant_id, customer_id, gateway, provider_reference,
                              amount_minor, currency, status)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
                '00000000-0000-0000-0000-0000000000c1'::uuid,
                'paystack','ref_1',500000,'NGN','SUCCESS');   -- no verified_at
    EXCEPTION WHEN check_violation THEN ok := true;
    END;
    PERFORM pg_temp.check('§18 SUCCESS payment requires verified_at (no redirect activation)',
        ok, 'unverified payment marked SUCCESS');

    INSERT INTO payments (tenant_id, customer_id, gateway, provider_reference,
                          amount_minor, currency, status, verified_at)
    VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
            '00000000-0000-0000-0000-0000000000c1'::uuid,
            'paystack','ref_2',500000,'NGN','SUCCESS', now());
    BEGIN
        INSERT INTO payments (tenant_id, customer_id, gateway, provider_reference,
                              amount_minor, currency, status, verified_at)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,
                '00000000-0000-0000-0000-0000000000c1'::uuid,
                'paystack','ref_2',500000,'NGN','SUCCESS', now());
    EXCEPTION WHEN unique_violation THEN dup := true;
    END;
    PERFORM pg_temp.check('§19 duplicate gateway reference cannot create a 2nd payment',
        dup, 'duplicate payment admitted');

    BEGIN
        UPDATE payments
           SET amount_minor = 1
         WHERE gateway = 'paystack' AND provider_reference = 'ref_2';
    EXCEPTION WHEN insufficient_privilege THEN immutable := true;
    END;
    PERFORM pg_temp.check('§89 successful payments are immutable forward facts',
        immutable, 'a successful payment was altered instead of requiring a refund record');
END$$;

-- ===========================================================================
-- §17/§32  ROUTER MANAGEMENT IP MUST BE PRIVATE
-- ===========================================================================
DO $$
DECLARE ok boolean := false; refok boolean := false;
BEGIN
    BEGIN
        INSERT INTO routers (tenant_id, name, management_ip, credential_ref, radius_secret_ref)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,'public-rtr','8.8.8.8',
                'netcore/x/api','netcore/x/radius');
    EXCEPTION WHEN check_violation THEN ok := true;
    END;
    PERFORM pg_temp.check('§17 router with a PUBLIC management IP is rejected', ok, '');

    BEGIN
        INSERT INTO routers (tenant_id, name, management_ip, credential_ref, radius_secret_ref)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,'leaky','10.0.0.9',
                'hunter2 plaintext password','netcore/x/radius');
    EXCEPTION WHEN check_violation THEN refok := true;
    END;
    PERFORM pg_temp.check('§33 a secret VALUE in a *_ref column is rejected', refok, '');
END$$;

-- ===========================================================================
-- §21A.3  PLAN COHERENCE
-- ===========================================================================
DO $$
DECLARE ok boolean := false;
BEGIN
    BEGIN
        INSERT INTO plans (tenant_id, name, price_minor, currency, duration_seconds,
                           download_bps, upload_bps, quota_bytes, quota_exhausted_action)
        VALUES ('00000000-0000-0000-0000-0000000000a1'::uuid,'bad',1,'NGN',60,
                1000,1000,1000,'THROTTLE');   -- THROTTLE with no throttle speeds
    EXCEPTION WHEN check_violation THEN ok := true;
    END;
    PERFORM pg_temp.check('§21A.3 THROTTLE plan without throttle speeds is rejected',
        ok, 'plan would grant FULL rate on exhaustion');
END$$;

-- ===========================================================================
-- §60.1  RADIUS ROLE GRANTS
-- ===========================================================================
DO $$
BEGIN
    PERFORM pg_temp.check('Phase 8 radius CANNOT read nas directly (uses ingestion function)',
        NOT has_table_privilege('netcore_radius','nas','SELECT'), '');
    PERFORM pg_temp.check('Phase 8 radius CANNOT update sessions directly',
        NOT has_column_privilege('netcore_radius','sessions','status','UPDATE'), '');
    PERFORM pg_temp.check('§60.1 radius CANNOT reassign sessions.customer_id',
        NOT has_column_privilege('netcore_radius','sessions','customer_id','UPDATE'), '');
    PERFORM pg_temp.check('§60.1 radius CANNOT read payments',
        NOT has_table_privilege('netcore_radius','payments','SELECT'), '');
    PERFORM pg_temp.check('§60.1 radius CANNOT read customers',
        NOT has_table_privilege('netcore_radius','customers','SELECT'), '');
    PERFORM pg_temp.check('v1.2 radius CANNOT read usage_counters directly (uses SECURITY DEFINER fn)',
        NOT has_table_privilege('netcore_radius','usage_counters','SELECT'), '');
    PERFORM pg_temp.check('v1.2 radius CAN execute radius_quota_lookup',
        has_function_privilege('netcore_radius','radius_quota_lookup(uuid,timestamptz)','EXECUTE'), '');
    PERFORM pg_temp.check('§13 radius CANNOT read portal_handoffs directly',
        NOT has_table_privilege('netcore_radius','portal_handoffs','SELECT'), '');
    PERFORM pg_temp.check('Phase 6 radius CANNOT call the inner portal nonce consumer',
        NOT has_function_privilege('netcore_radius','radius_portal_handoff_consume(text,inet,text)','EXECUTE'), '');
    PERFORM pg_temp.check('Phase 6 radius CAN atomically authorize a consumed portal handoff',
        has_function_privilege('netcore_radius','radius_portal_handoff_authorize(text,inet,text,timestamp with time zone)','EXECUTE'), '');
    PERFORM pg_temp.check('Phase 8 radius CAN atomically ingest accounting',
        has_function_privilege('netcore_radius','radius_accounting_ingest(inet,text,text,text,text,bigint,bigint,bigint,bigint,bigint,bigint,text,text,text)','EXECUTE'), '');
    PERFORM pg_temp.check('Phase 7 app role CAN resolve only payment webhook ownership',
        has_function_privilege('netcore_app_rw','payment_webhook_owner(text,text)','EXECUTE'), '');
    PERFORM pg_temp.check('Phase 7 app role CAN write only fixed payment webhook audit facts',
        has_function_privilege('netcore_app_rw','payment_webhook_audit(uuid,text,jsonb)','EXECUTE'), '');
    PERFORM pg_temp.check('Phase 7 radius CANNOT resolve payment webhook ownership',
        NOT has_function_privilege('netcore_radius','payment_webhook_owner(text,text)','EXECUTE'), '');
    PERFORM pg_temp.check('§39 app_rw CANNOT update audit_logs',
        NOT has_table_privilege('netcore_app_rw','audit_logs','UPDATE'), '');
    PERFORM pg_temp.check('§39 app_rw CANNOT delete accounting_records',
        NOT has_table_privilege('netcore_app_rw','accounting_records','DELETE'), '');
END$$;

-- ===========================================================================
-- §10  ROW-LEVEL SECURITY
-- ===========================================================================
DO $$
DECLARE n int;
BEGIN
    PERFORM pg_temp.check('§10 RLS enabled + FORCED on customers',
        (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname='customers'), '');
    PERFORM pg_temp.check('Phase 2 RLS enabled + FORCED on auth_sessions',
        (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname='auth_sessions'), '');
    PERFORM pg_temp.check('Phase 2 RLS enabled + FORCED on user_mfa_totp',
        (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname='user_mfa_totp'), '');
    PERFORM pg_temp.check('Portal handoffs have forced tenant RLS',
        (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname='portal_handoffs'), '');
    PERFORM pg_temp.check('Payment request keys have forced tenant RLS',
        (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname='idempotency_keys'), '');
    SELECT count(*) INTO n FROM pg_policies
     WHERE tablename IN ('customers','subscriptions','payments','invoices','sessions',
                         'usage_counters','ledger_entries','devices','vouchers','plans',
                         'subscription_events','outbox_events','users','roles','user_roles',
                         'role_permissions','auth_sessions','audit_logs','user_mfa_totp',
                         'automation_workflows','portal_handoffs','idempotency_keys')
       AND policyname='tenant_isolation';
    PERFORM pg_temp.check('Tenant isolation policy present on all 22 scoped tables',
        n = 22, format('only %s of 22 tables have the policy', n));
END$$;

-- ===========================================================================
-- Results
-- ===========================================================================
\set QUIET off
SELECT name, verdict FROM results ORDER BY n;

SELECT count(*) FILTER (WHERE verdict = 'PASS')       AS passed,
       count(*) FILTER (WHERE verdict LIKE 'FAIL%')   AS failed
FROM results;

-- Non-zero exit if anything failed (CI gate)
DO $$
DECLARE f int;
BEGIN
    SELECT count(*) INTO f FROM results WHERE verdict LIKE 'FAIL%';
    IF f > 0 THEN
        RAISE EXCEPTION '% invariant test(s) FAILED', f;
    END IF;
END$$;
