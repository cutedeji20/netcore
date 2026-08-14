-- 0030_radius_accounting_ingest.up.sql
--
-- Router accounting is untrusted network input.  FreeRADIUS receives it but
-- must not receive broad access to tenant, subscription, session, or quota
-- tables in order to persist it.  This one SECURITY DEFINER surface verifies
-- the NAS and the Class value issued at authorization, deduplicates packet
-- retransmits, then applies the cumulative traffic watermark atomically.

BEGIN;

CREATE FUNCTION radius_accounting_ingest(
    p_nas_address          inet,
    p_status_type          text,
    p_acct_session_id      text,
    p_acct_unique_id       text,
    p_class                text,
    p_event_epoch          bigint,
    p_acct_session_time    bigint,
    p_input_octets         bigint,
    p_input_gigawords      bigint,
    p_output_octets        bigint,
    p_output_gigawords     bigint,
    p_framed_ip_address    text DEFAULT NULL,
    p_calling_station_id   text DEFAULT NULL,
    p_terminate_cause      text DEFAULT NULL
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    v_status              text := upper(replace(COALESCE(p_status_type, ''), '-', '_'));
    v_record_status       text;
    v_tenant_id           uuid;
    v_router_id           uuid;
    v_subscription_id     uuid;
    v_customer_id         uuid;
    v_device_id           uuid;
    v_session_id          uuid;
    v_session_status      text;
    v_session_acct_id     text;
    v_session_started_at  timestamptz;
    v_event_id            uuid;
    v_record_id           uuid;
    v_event_at            timestamptz;
    v_started_at          timestamptz;
    v_framed_ip           inet;
    v_normalized_mac      text;
    v_input_total         bigint;
    v_output_total        bigint;
    v_cumulative          bigint;
    v_sessions_closed     integer := 0;
BEGIN
    -- Event-Timestamp is an unsigned RADIUS date.  It is deliberately kept as
    -- packet time (rather than the database clock) so quota_apply chooses the
    -- correct counter around a quota-reset boundary.
    IF p_nas_address IS NULL OR p_event_epoch IS NULL OR p_event_epoch <= 0
       OR p_acct_session_time IS NULL OR p_acct_session_time < 0
       OR p_input_octets IS NULL OR p_input_octets < 0 OR p_input_octets > 4294967295
       OR p_output_octets IS NULL OR p_output_octets < 0 OR p_output_octets > 4294967295
       -- accounting_records uses signed bigint.  A high word above 2^31-1
       -- cannot be represented without corrupting the cumulative total.
       OR p_input_gigawords IS NULL OR p_input_gigawords < 0 OR p_input_gigawords > 2147483647
       OR p_output_gigawords IS NULL OR p_output_gigawords < 0 OR p_output_gigawords > 2147483647
    THEN
        RETURN false;
    END IF;

    SELECT n.tenant_id, n.router_id
      INTO v_tenant_id, v_router_id
      FROM nas AS n
     WHERE n.nasname = p_nas_address
       AND n.status = 'ACTIVE'
       AND n.router_id IS NOT NULL
     FOR KEY SHARE;

    IF NOT FOUND THEN
        -- The RADIUS client declaration is the first boundary.  This second
        -- lookup makes a stale/disabled registration unable to write usage.
        RETURN false;
    END IF;

    v_event_at := to_timestamp(p_event_epoch);

    -- Accounting-On/Off have no Acct-Session-Id or Class.  They are stored
    -- separately and close only sessions that existed at the NAS event time.
    IF v_status IN ('ACCOUNTING_ON', 'ACCOUNTING_OFF') THEN
        INSERT INTO nas_accounting_events (
            tenant_id, router_id, nas_ip_address, acct_status_type, event_timestamp
        ) VALUES (
            v_tenant_id, v_router_id, p_nas_address,
            CASE v_status WHEN 'ACCOUNTING_ON' THEN 'ON' ELSE 'OFF' END,
            v_event_at
        )
        ON CONFLICT (nas_ip_address, acct_status_type, event_timestamp) DO NOTHING
        RETURNING id INTO v_event_id;

        -- Duplicate On/Off packets have already closed the relevant sessions.
        IF v_event_id IS NULL THEN
            RETURN true;
        END IF;

        UPDATE sessions
           SET status = 'CLOSED',
               ended_at = v_event_at,
               close_reason = 'ACCT_ON_OFF',
               terminate_cause = CASE WHEN v_status = 'ACCOUNTING_ON'
                                      THEN 'NAS_RESTART'
                                      ELSE 'NAS_SHUTDOWN' END,
               updated_at = now()
         WHERE router_id = v_router_id
           AND status <> 'CLOSED'
           AND started_at <= v_event_at;
        GET DIAGNOSTICS v_sessions_closed = ROW_COUNT;

        UPDATE nas_accounting_events
           SET sessions_closed = v_sessions_closed,
               processed_at = now()
         WHERE id = v_event_id;
        RETURN true;
    END IF;

    IF v_status NOT IN ('START', 'INTERIM_UPDATE', 'STOP')
       OR p_acct_session_id IS NULL OR length(p_acct_session_id) NOT BETWEEN 1 AND 128
       OR p_acct_unique_id IS NULL OR length(p_acct_unique_id) NOT BETWEEN 1 AND 128
       OR p_acct_session_id ~ '[[:cntrl:]]' OR p_acct_unique_id ~ '[[:cntrl:]]'
       -- Class is opaque to RouterOS but must be exactly the value our
       -- authorize policy emitted.  Do not accept a browser- or NAS-selected
       -- subscription id here.
       OR p_class !~ '^netcore:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
    THEN
        RETURN false;
    END IF;

    v_subscription_id := lower(substring(p_class FROM 9))::uuid;

    SELECT s.customer_id
      INTO v_customer_id
      FROM subscriptions AS s
     WHERE s.id = v_subscription_id
       AND s.tenant_id = v_tenant_id
     FOR KEY SHARE;
    IF NOT FOUND THEN
        RETURN false;
    END IF;

    BEGIN
        v_framed_ip := NULLIF(p_framed_ip_address, '')::inet;
    EXCEPTION WHEN invalid_text_representation THEN
        RETURN false;
    END;

    v_normalized_mac := regexp_replace(lower(COALESCE(p_calling_station_id, '')),
                                        '[^0-9a-f]', '', 'g');
    IF v_normalized_mac !~ '^[0-9a-f]{12}$' THEN
        v_normalized_mac := NULL;
    END IF;

    IF v_normalized_mac IS NOT NULL THEN
        SELECT d.id
          INTO v_device_id
          FROM devices AS d
         WHERE d.tenant_id = v_tenant_id
           AND d.normalized_mac = v_normalized_mac
           AND d.status = 'ACTIVE'
         LIMIT 1;
    END IF;

    -- Reassemble each unsigned 64-bit RADIUS counter before summing directions.
    -- The signed bigint model cannot represent values at or over 2^63; refuse
    -- rather than wrap and silently under-bill.
    v_input_total := p_input_gigawords * 4294967296 + p_input_octets;
    v_output_total := p_output_gigawords * 4294967296 + p_output_octets;
    IF v_input_total > 9223372036854775807 - v_output_total THEN
        RETURN false;
    END IF;
    v_cumulative := v_input_total + v_output_total;
    v_record_status := CASE v_status WHEN 'INTERIM_UPDATE' THEN 'INTERIM' ELSE v_status END;
    v_started_at := v_event_at - (p_acct_session_time * interval '1 second');

    -- A unique accounting id binds every retransmit to one logical session.
    -- Guard an active Acct-Session-Id collision before the partial unique
    -- index has to raise a less actionable error.
    SELECT s.id, s.status, s.acct_session_id, s.started_at
      INTO v_session_id, v_session_status, v_session_acct_id, v_session_started_at
      FROM sessions AS s
     WHERE s.router_id = v_router_id
       AND s.acct_unique_id = p_acct_unique_id
     ORDER BY s.started_at DESC
     LIMIT 1
     FOR UPDATE;

    IF FOUND AND v_session_acct_id <> p_acct_session_id THEN
        RETURN false;
    END IF;

    IF v_session_id IS NULL THEN
        IF EXISTS (
            SELECT 1
              FROM sessions AS s
             WHERE s.router_id = v_router_id
               AND s.acct_session_id = p_acct_session_id
               AND s.status <> 'CLOSED'
        ) THEN
            RETURN false;
        END IF;

        IF v_record_status = 'STOP' THEN
            INSERT INTO sessions (
                tenant_id, customer_id, subscription_id, device_id, router_id,
                acct_session_id, acct_unique_id, nas_ip_address, ip_address,
                mac_address, normalized_mac, started_at, last_interim_at,
                ended_at, duration_seconds, terminate_cause, close_reason, status
            ) VALUES (
                v_tenant_id, v_customer_id, v_subscription_id, v_device_id, v_router_id,
                p_acct_session_id, p_acct_unique_id, p_nas_address, v_framed_ip,
                NULLIF(p_calling_station_id, ''), v_normalized_mac, v_started_at, v_event_at,
                v_event_at, p_acct_session_time, NULLIF(p_terminate_cause, ''), 'STOP', 'CLOSED'
            ) RETURNING id, status, started_at
              INTO v_session_id, v_session_status, v_session_started_at;
        ELSE
            INSERT INTO sessions (
                tenant_id, customer_id, subscription_id, device_id, router_id,
                acct_session_id, acct_unique_id, nas_ip_address, ip_address,
                mac_address, normalized_mac, started_at, last_interim_at, status
            ) VALUES (
                v_tenant_id, v_customer_id, v_subscription_id, v_device_id, v_router_id,
                p_acct_session_id, p_acct_unique_id, p_nas_address, v_framed_ip,
                NULLIF(p_calling_station_id, ''), v_normalized_mac, v_started_at, v_event_at, 'ACTIVE'
            ) RETURNING id, status, started_at
              INTO v_session_id, v_session_status, v_session_started_at;
        END IF;
    END IF;

    INSERT INTO accounting_records (
        tenant_id, session_id, customer_id, subscription_id, device_id, router_id,
        acct_session_id, acct_unique_id, acct_status_type, event_timestamp,
        acct_session_time, start_time, stop_time, input_octets, output_octets,
        input_gigawords, output_gigawords, terminate_cause
    ) VALUES (
        v_tenant_id, v_session_id, v_customer_id, v_subscription_id, v_device_id, v_router_id,
        p_acct_session_id, p_acct_unique_id, v_record_status, v_event_at,
        p_acct_session_time, v_session_started_at,
        CASE WHEN v_record_status = 'STOP' THEN v_event_at END,
        p_input_octets, p_output_octets, p_input_gigawords, p_output_gigawords,
        NULLIF(p_terminate_cause, '')
    )
    ON CONFLICT ON CONSTRAINT accounting_dedup_key DO NOTHING
    RETURNING id INTO v_record_id;

    -- A retransmitted packet must not alter session state or charge quota a
    -- second time.  The accounting record's stable packet-time dedup key is
    -- the authoritative replay boundary.
    IF v_record_id IS NULL THEN
        RETURN true;
    END IF;

    PERFORM quota_apply(v_subscription_id, p_acct_unique_id, v_event_at, v_cumulative);

    IF v_record_status = 'STOP' THEN
        UPDATE sessions
           SET last_interim_at = GREATEST(last_interim_at, v_event_at),
               ended_at = v_event_at,
               duration_seconds = p_acct_session_time,
               terminate_cause = NULLIF(p_terminate_cause, ''),
               close_reason = 'STOP',
               status = 'CLOSED',
               updated_at = now()
         WHERE id = v_session_id
           AND status <> 'CLOSED';
    ELSE
        UPDATE sessions
           SET last_interim_at = GREATEST(last_interim_at, v_event_at),
               status = 'ACTIVE',
               updated_at = now()
         WHERE id = v_session_id
           AND status <> 'CLOSED';
    END IF;

    RETURN true;
END;
$$;

REVOKE ALL ON FUNCTION radius_accounting_ingest(
    inet, text, text, text, text, bigint, bigint, bigint, bigint, bigint,
    bigint, text, text, text
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION radius_accounting_ingest(
    inet, text, text, text, text, bigint, bigint, bigint, bigint, bigint,
    bigint, text, text, text
) TO netcore_radius;

COMMENT ON FUNCTION radius_accounting_ingest IS
'The sole RADIUS accounting write surface. Verifies an active NAS and the Class '
'issued by portal authorization; atomically stores Start/Interim/Stop or NAS '
'On/Off, reassembles gigawords, deduplicates retransmits, and applies quota.';

-- The original 0004 grants existed before this narrow ingestion function.  A
-- RADIUS LOGIN account now needs only the two portal/accounting functions,
-- not direct table access or direct quota mutation.
REVOKE SELECT ON nas, sessions FROM netcore_radius;
REVOKE UPDATE (status, ended_at, close_reason, terminate_cause, last_interim_at)
    ON sessions FROM netcore_radius;
REVOKE INSERT ON accounting_records, nas_accounting_events FROM netcore_radius;
REVOKE EXECUTE ON FUNCTION quota_apply(uuid, text, timestamptz, bigint) FROM netcore_radius;

COMMIT;
