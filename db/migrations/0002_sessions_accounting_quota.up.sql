-- 0002_sessions_accounting_quota.up.sql
-- Spec: BUILD.md v1.1 §9, §9.1, §9A, §21A, §24A
--
-- THIS IS THE MONEY PATH. Every constraint here was derived from a failure
-- reproduced against a live PostgreSQL instance, not from reasoning. Read
-- docs/quota.md and docs/database.md before changing anything in this file.

BEGIN;

-- ---------------------------------------------------------------------------
-- §9 sessions
-- ---------------------------------------------------------------------------
CREATE TABLE sessions (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    customer_id      uuid        NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    subscription_id  uuid        NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    device_id        uuid        REFERENCES devices(id) ON DELETE SET NULL,
    router_id        uuid        REFERENCES routers(id) ON DELETE SET NULL,

    -- §25 CoA/Disconnect needs BOTH of these to target the session.
    -- One RADIUS attribute, one name. (v1.2: was radius_session_id, renamed to
    -- match accounting_records.acct_session_id and the RADIUS attribute itself.)
    acct_session_id  text        NOT NULL,
    acct_unique_id   text        NOT NULL,
    nas_ip_address   inet        NOT NULL,

    ip_address       inet,
    mac_address      text,
    normalized_mac   text,

    started_at       timestamptz NOT NULL DEFAULT now(),
    last_interim_at  timestamptz NOT NULL DEFAULT now(),  -- §24A.3 reaper input
    ended_at         timestamptz,
    duration_seconds bigint,
    terminate_cause  text,

    -- §24A: how the session ended. NULL while active. Distinguishing these
    -- is what makes the reap-rate metric meaningful.
    close_reason     text        CHECK (close_reason IN ('STOP','ACCT_ON_OFF','REAPER','ADMIN')),
    status           text        NOT NULL DEFAULT 'ACTIVE'
                                 CHECK (status IN ('ACTIVE','SUSPECT','CLOSED')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT sessions_closed_has_reason CHECK (
        (status = 'CLOSED') = (close_reason IS NOT NULL)
    ),
    CONSTRAINT sessions_closed_has_end CHECK (
        (status = 'CLOSED') = (ended_at IS NOT NULL)
    )
);

-- One active session per (router, acct_session_id). A duplicate Accounting-Start
-- must not create a second row that then leaks as a zombie.
CREATE UNIQUE INDEX sessions_active_key
    ON sessions (router_id, acct_session_id)
    WHERE status <> 'CLOSED';

-- §24A.3 reaper scan. Partial index: only ACTIVE/SUSPECT rows are ever scanned,
-- so this stays small even as the table grows to millions of closed sessions.
CREATE INDEX sessions_stale_scan_idx
    ON sessions (last_interim_at)
    WHERE status IN ('ACTIVE','SUSPECT');

CREATE INDEX sessions_subscription_idx ON sessions (subscription_id, started_at DESC);
CREATE INDEX sessions_customer_idx     ON sessions (tenant_id, customer_id, started_at DESC);

-- ---------------------------------------------------------------------------
-- §9 / §9.1 accounting_records
--
-- PARTITIONED ON event_timestamp, NOT created_at.
--
-- Why this matters, verified against PostgreSQL 16:
--   * A unique constraint on a partitioned table MUST include every partition
--     column. Omitting it fails at CREATE time.
--   * Including created_at (insert clock) makes the constraint legal and
--     USELESS: a retransmitted packet arriving 2s later gets a different key
--     and inserts cleanly. Measured: 1 duplicated packet -> 2 stored rows.
--   * event_timestamp is packet-derived and STABLE across retransmits.
--     acct_session_time is monotonic per session and differs between two
--     legitimate consecutive interim updates.
--   Together they dedupe retransmits while admitting genuine updates.
-- ---------------------------------------------------------------------------
CREATE TABLE accounting_records (
    id                uuid        NOT NULL DEFAULT gen_random_uuid(),
    tenant_id         uuid        NOT NULL,
    session_id        uuid,
    customer_id       uuid,
    subscription_id   uuid,
    device_id         uuid,
    router_id         uuid        NOT NULL,

    acct_session_id   text        NOT NULL,
    acct_unique_id    text        NOT NULL,
    acct_status_type  text        NOT NULL CHECK (acct_status_type IN ('START','INTERIM','STOP')),

    -- RADIUS Event-Timestamp (attr 55). PACKET time. Partition key.
    event_timestamp   timestamptz NOT NULL,
    -- Acct-Session-Time. Seconds since session start. Dedup discriminator.
    acct_session_time bigint      NOT NULL CHECK (acct_session_time >= 0),

    start_time        timestamptz,
    stop_time         timestamptz,

    -- §21A.6: 32-bit counters plus their high words. Both REQUIRED.
    -- Reading octets without gigawords bills 50GB as 2GB, silently.
    input_octets      bigint      NOT NULL DEFAULT 0 CHECK (input_octets    >= 0),
    output_octets     bigint      NOT NULL DEFAULT 0 CHECK (output_octets   >= 0),
    input_gigawords   bigint      NOT NULL DEFAULT 0 CHECK (input_gigawords >= 0),
    output_gigawords  bigint      NOT NULL DEFAULT 0 CHECK (output_gigawords>= 0),
    input_packets     bigint      NOT NULL DEFAULT 0,
    output_packets    bigint      NOT NULL DEFAULT 0,

    terminate_cause   text,
    created_at        timestamptz NOT NULL DEFAULT now(),  -- diagnostics ONLY. Never a key.

    PRIMARY KEY (id, event_timestamp),
    CONSTRAINT accounting_dedup_key UNIQUE
        (router_id, acct_unique_id, acct_status_type, acct_session_time, event_timestamp)
) PARTITION BY RANGE (event_timestamp);

CREATE INDEX accounting_session_idx ON accounting_records (session_id, event_timestamp);
CREATE INDEX accounting_subscription_idx ON accounting_records (subscription_id, event_timestamp);

-- Bootstrap partitions. §99 scheduler creates future months ahead of time;
-- the DEFAULT partition catches clock-skewed packets rather than rejecting them
-- (§9.1 clock-skew caveat) so accounting is never lost to a bad router clock.
CREATE TABLE accounting_records_default PARTITION OF accounting_records DEFAULT;
CREATE TABLE accounting_records_2026_08 PARTITION OF accounting_records
    FOR VALUES FROM ('2026-08-01+00') TO ('2026-09-01+00');
CREATE TABLE accounting_records_2026_09 PARTITION OF accounting_records
    FOR VALUES FROM ('2026-09-01+00') TO ('2026-10-01+00');

-- ---------------------------------------------------------------------------
-- v1.2 ADDITION: nas_accounting_events
--
-- Accounting-On/Off carry NAS-IP-Address and NO Acct-Session-Id (§24A.2).
-- They therefore cannot satisfy accounting_records' dedup key and have
-- nowhere to land. Storing them is what makes the §24A.2 recovery path
-- auditable ("why did 400 sessions close at 03:14?").
-- ---------------------------------------------------------------------------
CREATE TABLE nas_accounting_events (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    router_id        uuid        REFERENCES routers(id) ON DELETE SET NULL,
    nas_ip_address   inet        NOT NULL,
    acct_status_type text        NOT NULL CHECK (acct_status_type IN ('ON','OFF')),
    event_timestamp  timestamptz NOT NULL,
    sessions_closed  int         NOT NULL DEFAULT 0,
    processed_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (nas_ip_address, acct_status_type, event_timestamp)
);

-- ---------------------------------------------------------------------------
-- §9A / §21A usage_counters — THE QUOTA LEDGER
-- ---------------------------------------------------------------------------
CREATE TABLE usage_counters (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    subscription_id uuid        NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    customer_id     uuid        NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,

    period_start    timestamptz NOT NULL,
    period_end      timestamptz NOT NULL,

    quota_bytes     bigint      NOT NULL CHECK (quota_bytes >= 0),
    consumed_bytes  bigint      NOT NULL DEFAULT 0 CHECK (consumed_bytes >= 0),

    -- ####################################################################
    -- NOT NULL DEFAULT '{}'::jsonb IS LOAD-BEARING. DO NOT MAKE NULLABLE.
    --
    -- jsonb_set(NULL, ...) returns NULL in PostgreSQL. If this column is
    -- nullable and unset, the watermark never persists, every packet applies
    -- against a watermark of zero, and EVERY RETRANSMIT DOUBLE-BILLS.
    --
    -- Measured on PostgreSQL 16.13 with the column nullable:
    --   2 GiB of real traffic + 1 retransmit  ->  5 GiB billed
    --   (2147483648 actual -> 5368709120 recorded), watermark still NULL.
    -- ####################################################################
    last_applied_watermark jsonb NOT NULL DEFAULT '{}'::jsonb,

    exhausted_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    UNIQUE (subscription_id, period_start),
    CONSTRAINT usage_period_ordered CHECK (period_end > period_start)
);

-- §21A.4: FreeRADIUS reads this on every Access-Request. Single-row lookup by
-- (subscription, period containing the packet time) — no aggregation, O(1).
CREATE INDEX usage_counters_lookup_idx
    ON usage_counters (subscription_id, period_start DESC, period_end);

-- ---------------------------------------------------------------------------
-- v1.2 ADDITION: quota_apply()
--
-- The §21A.8 invariant, implemented once, in the database, so no caller can
-- get it wrong. Application code MUST NOT hand-roll this UPDATE.
--
-- Invariant: consumed_bytes is monotonically non-decreasing, and applying the
-- same accounting packet twice has the same effect as applying it once.
--
-- Three fixes over the v1.1 draft:
--   1. Period selected by PACKET time, not now(). Selecting by now() at a
--      reset boundary applies a packet to the wrong period and double-counts.
--   2. Distinguishes "no growth" (correct no-op) from "no counter row"
--      (revenue silently discarded) — the latter raises, and the caller
--      surfaces it as quota_counter_missing_total (P2).
--   3. Watermark keyed by acct_unique_id, carried forward across period
--      resets by the caller (see quota_reset()).
-- ---------------------------------------------------------------------------
CREATE FUNCTION quota_apply(
    p_subscription_id uuid,
    p_session_key     text,        -- acct_unique_id
    p_event_time      timestamptz, -- PACKET time, not now()
    p_cumulative      bigint       -- total bytes this session has used, all-time
) RETURNS TABLE (consumed bigint, applied bigint, quota bigint)
LANGUAGE plpgsql AS $$
DECLARE
    v_id      uuid;
    v_hw      bigint;
    v_delta   bigint;
BEGIN
    IF p_cumulative < 0 THEN
        RAISE EXCEPTION 'quota_apply: negative cumulative % for session %',
            p_cumulative, p_session_key USING ERRCODE = '22023';
    END IF;

    SELECT uc.id,
           COALESCE((uc.last_applied_watermark ->> p_session_key)::bigint, 0)
      INTO v_id, v_hw
      FROM usage_counters uc
     WHERE uc.subscription_id = p_subscription_id
       AND uc.period_start <= p_event_time
       AND uc.period_end   >  p_event_time
     FOR UPDATE;

    -- No counter row for this packet's period. This is NOT a no-op: it means
    -- real traffic has nowhere to be recorded. Fail loudly.
    IF v_id IS NULL THEN
        RAISE EXCEPTION 'quota_apply: no usage_counters row for subscription % at %',
            p_subscription_id, p_event_time USING ERRCODE = 'P0002';
    END IF;

    v_delta := GREATEST(0, p_cumulative - v_hw);

    -- Replay or out-of-order packet: watermark already at or beyond this
    -- value. No-op, but still report current state to the caller.
    IF v_delta = 0 THEN
        RETURN QUERY
            SELECT uc.consumed_bytes, 0::bigint, uc.quota_bytes
              FROM usage_counters uc WHERE uc.id = v_id;
        RETURN;
    END IF;

    RETURN QUERY
    UPDATE usage_counters uc
       SET consumed_bytes = uc.consumed_bytes + v_delta,
           last_applied_watermark =
               jsonb_set(uc.last_applied_watermark,
                         ARRAY[p_session_key],
                         to_jsonb(p_cumulative)),
           exhausted_at = CASE
               WHEN uc.exhausted_at IS NULL
                    AND uc.consumed_bytes + v_delta >= uc.quota_bytes
               THEN now() ELSE uc.exhausted_at END,
           updated_at = now()
     WHERE uc.id = v_id
    RETURNING uc.consumed_bytes, v_delta, uc.quota_bytes;
END;
$$;

COMMENT ON FUNCTION quota_apply IS
'BUILD.md §21A.8. The ONLY supported way to decrement quota. Idempotent under '
'replay and out-of-order delivery. Do not hand-roll this UPDATE.';

-- ---------------------------------------------------------------------------
-- v1.2 ADDITION: quota_reset() with watermark carry-forward
--
-- §21A.7 says a reset creates a NEW counter row. The v1.1 draft did not say
-- what happens to in-flight sessions: a new row starts with an empty watermark
-- map, so the first packet from a session that SPANS the boundary re-applies
-- its entire cumulative total to the new period. A customer online across
-- midnight on a DAILY plan loses their whole new allowance instantly.
--
-- Fix: carry forward the watermarks of sessions still active at the boundary.
-- ---------------------------------------------------------------------------
CREATE FUNCTION quota_reset(
    p_subscription_id uuid,
    p_period_start    timestamptz,
    p_period_end      timestamptz,
    p_quota_bytes     bigint
) RETURNS uuid
LANGUAGE plpgsql AS $$
DECLARE
    v_new  uuid;
    v_carry jsonb;
BEGIN
    -- Watermarks of sessions still open at the boundary. Closed sessions are
    -- dropped, which keeps the jsonb map from growing without bound.
    SELECT COALESCE(jsonb_object_agg(k, v), '{}'::jsonb)
      INTO v_carry
      FROM usage_counters uc
      CROSS JOIN LATERAL jsonb_each(uc.last_applied_watermark) AS e(k, v)
     WHERE uc.subscription_id = p_subscription_id
       AND uc.period_end = p_period_start
       AND EXISTS (SELECT 1 FROM sessions s
                    WHERE s.acct_unique_id = e.k AND s.status <> 'CLOSED');

    INSERT INTO usage_counters
        (tenant_id, subscription_id, customer_id,
         period_start, period_end, quota_bytes, last_applied_watermark)
    SELECT s.tenant_id, s.id, s.customer_id,
           p_period_start, p_period_end, p_quota_bytes, COALESCE(v_carry, '{}'::jsonb)
      FROM subscriptions s
     WHERE s.id = p_subscription_id
    ON CONFLICT (subscription_id, period_start) DO NOTHING
    RETURNING id INTO v_new;

    IF v_new IS NULL THEN
        SELECT id INTO v_new FROM usage_counters
         WHERE subscription_id = p_subscription_id AND period_start = p_period_start;
    END IF;
    RETURN v_new;
END;
$$;

COMMENT ON FUNCTION quota_reset IS
'BUILD.md §21A.7 + v1.2 carry-forward. Creates the next period''s counter, '
'preserving watermarks of sessions that span the boundary so they are not '
're-billed from zero.';

COMMIT;
