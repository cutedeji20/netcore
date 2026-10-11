BEGIN;

-- Approved maintenance-window lock: no accounting insert may land between
-- the historical snapshot and installation of the accounting trigger.
LOCK TABLE accounting_records IN SHARE ROW EXCLUSIVE MODE;


-- One high watermark per RADIUS accounting session. Replayed or older packets
-- cannot increase the daily usage total.
CREATE TABLE router_accounting_watermarks (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    router_id uuid NOT NULL REFERENCES routers(id) ON DELETE RESTRICT,
    acct_unique_id text NOT NULL,
    input_bytes bigint NOT NULL DEFAULT 0 CHECK (input_bytes >= 0),
    output_bytes bigint NOT NULL DEFAULT 0 CHECK (output_bytes >= 0),
    PRIMARY KEY (tenant_id, router_id, acct_unique_id)
);
ALTER TABLE router_accounting_watermarks ENABLE ROW LEVEL SECURITY;
ALTER TABLE router_accounting_watermarks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON router_accounting_watermarks USING (tenant_id = current_tenant_id()) WITH CHECK (tenant_id = current_tenant_id());

CREATE TABLE router_usage_daily (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    router_id uuid NOT NULL REFERENCES routers(id) ON DELETE RESTRICT,
    usage_day date NOT NULL,
    input_bytes bigint NOT NULL DEFAULT 0 CHECK (input_bytes >= 0),
    output_bytes bigint NOT NULL DEFAULT 0 CHECK (output_bytes >= 0),
    PRIMARY KEY (tenant_id, router_id, usage_day)
);
ALTER TABLE router_usage_daily ENABLE ROW LEVEL SECURITY;
ALTER TABLE router_usage_daily FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON router_usage_daily USING (tenant_id = current_tenant_id()) WITH CHECK (tenant_id = current_tenant_id());
GRANT SELECT ON router_usage_daily TO netcore_app_rw, netcore_app_ro;

-- Backfill the historical high-water deltas before enabling the insert trigger.
WITH ordered AS (
    SELECT tenant_id, router_id, acct_unique_id, event_timestamp,
           input_gigawords * 4294967296 + input_octets AS input_total,
           output_gigawords * 4294967296 + output_octets AS output_total
      FROM accounting_records
), prior AS (
    SELECT *,
           COALESCE(max(input_total) OVER (PARTITION BY tenant_id, router_id, acct_unique_id ORDER BY event_timestamp, input_total, output_total ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING), 0) AS prior_input,
           COALESCE(max(output_total) OVER (PARTITION BY tenant_id, router_id, acct_unique_id ORDER BY event_timestamp, input_total, output_total ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING), 0) AS prior_output
      FROM ordered
)
INSERT INTO router_usage_daily (tenant_id, router_id, usage_day, input_bytes, output_bytes)
SELECT tenant_id, router_id, (event_timestamp AT TIME ZONE 'Africa/Lagos')::date,
       sum(greatest(input_total - prior_input, 0))::bigint,
       sum(greatest(output_total - prior_output, 0))::bigint
  FROM prior
 GROUP BY tenant_id, router_id, (event_timestamp AT TIME ZONE 'Africa/Lagos')::date;

INSERT INTO router_accounting_watermarks (tenant_id, router_id, acct_unique_id, input_bytes, output_bytes)
SELECT tenant_id, router_id, acct_unique_id,
       max(input_gigawords * 4294967296 + input_octets),
       max(output_gigawords * 4294967296 + output_octets)
  FROM accounting_records
 GROUP BY tenant_id, router_id, acct_unique_id;

CREATE FUNCTION router_usage_accounting_insert() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_input bigint := NEW.input_gigawords * 4294967296 + NEW.input_octets;
    v_output bigint := NEW.output_gigawords * 4294967296 + NEW.output_octets;
    v_prior_input bigint;
    v_prior_output bigint;
    v_delta_input bigint;
    v_delta_output bigint;
BEGIN
    INSERT INTO router_accounting_watermarks (tenant_id, router_id, acct_unique_id)
    VALUES (NEW.tenant_id, NEW.router_id, NEW.acct_unique_id)
    ON CONFLICT DO NOTHING;
    SELECT input_bytes, output_bytes INTO v_prior_input, v_prior_output
      FROM router_accounting_watermarks
     WHERE tenant_id=NEW.tenant_id AND router_id=NEW.router_id AND acct_unique_id=NEW.acct_unique_id
     FOR UPDATE;
    v_delta_input := greatest(v_input - v_prior_input, 0);
    v_delta_output := greatest(v_output - v_prior_output, 0);
    IF v_delta_input > 0 OR v_delta_output > 0 THEN
        UPDATE router_accounting_watermarks
           SET input_bytes=greatest(input_bytes, v_input), output_bytes=greatest(output_bytes, v_output)
         WHERE tenant_id=NEW.tenant_id AND router_id=NEW.router_id AND acct_unique_id=NEW.acct_unique_id;
        INSERT INTO router_usage_daily (tenant_id, router_id, usage_day, input_bytes, output_bytes)
        VALUES (NEW.tenant_id, NEW.router_id, (NEW.event_timestamp AT TIME ZONE 'Africa/Lagos')::date, v_delta_input, v_delta_output)
        ON CONFLICT (tenant_id, router_id, usage_day) DO UPDATE
        SET input_bytes=router_usage_daily.input_bytes + EXCLUDED.input_bytes,
            output_bytes=router_usage_daily.output_bytes + EXCLUDED.output_bytes;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER router_usage_accounting_insert
AFTER INSERT ON accounting_records
FOR EACH ROW EXECUTE FUNCTION router_usage_accounting_insert();
REVOKE ALL ON FUNCTION router_usage_accounting_insert() FROM PUBLIC;

-- RouterOS samples are independent of RADIUS accounting and have no customer
-- attribution. The collector supplies these only over the private management path.
CREATE TABLE router_telemetry_samples (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    router_id uuid NOT NULL REFERENCES routers(id) ON DELETE RESTRICT,
    sampled_at timestamptz NOT NULL,
    wan_interface text NOT NULL,
    wan_rx_bytes bigint NOT NULL CHECK (wan_rx_bytes >= 0),
    wan_tx_bytes bigint NOT NULL CHECK (wan_tx_bytes >= 0),
    observed_hotspot_hosts integer NOT NULL CHECK (observed_hotspot_hosts >= 0),
    PRIMARY KEY (tenant_id, router_id, sampled_at)
);
CREATE INDEX router_telemetry_latest_idx ON router_telemetry_samples(tenant_id, router_id, sampled_at DESC);
ALTER TABLE router_telemetry_samples ENABLE ROW LEVEL SECURITY;
ALTER TABLE router_telemetry_samples FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON router_telemetry_samples USING (tenant_id = current_tenant_id()) WITH CHECK (tenant_id = current_tenant_id());
GRANT SELECT, INSERT ON router_telemetry_samples TO netcore_app_rw;
GRANT SELECT ON router_telemetry_samples TO netcore_app_ro;

CREATE TABLE router_wan_daily (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    router_id uuid NOT NULL REFERENCES routers(id) ON DELETE RESTRICT,
    usage_day date NOT NULL,
    rx_bytes bigint NOT NULL DEFAULT 0 CHECK (rx_bytes >= 0),
    tx_bytes bigint NOT NULL DEFAULT 0 CHECK (tx_bytes >= 0),
    PRIMARY KEY (tenant_id, router_id, usage_day)
);
ALTER TABLE router_wan_daily ENABLE ROW LEVEL SECURITY;
ALTER TABLE router_wan_daily FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON router_wan_daily USING (tenant_id = current_tenant_id()) WITH CHECK (tenant_id = current_tenant_id());
GRANT SELECT ON router_wan_daily TO netcore_app_rw, netcore_app_ro;

CREATE FUNCTION router_wan_sample_insert() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_previous router_telemetry_samples%ROWTYPE;
    v_rx bigint;
    v_tx bigint;
BEGIN
    SELECT * INTO v_previous FROM router_telemetry_samples
     WHERE tenant_id=NEW.tenant_id AND router_id=NEW.router_id AND sampled_at < NEW.sampled_at
     ORDER BY sampled_at DESC LIMIT 1;
    IF NOT FOUND THEN RETURN NEW; END IF;
    -- A counter reset means that interval is unknown; resume at the next poll.
    v_rx := greatest(NEW.wan_rx_bytes - v_previous.wan_rx_bytes, 0);
    v_tx := greatest(NEW.wan_tx_bytes - v_previous.wan_tx_bytes, 0);
    IF v_rx > 0 OR v_tx > 0 THEN
        INSERT INTO router_wan_daily (tenant_id, router_id, usage_day, rx_bytes, tx_bytes)
        VALUES (NEW.tenant_id, NEW.router_id, (NEW.sampled_at AT TIME ZONE 'Africa/Lagos')::date, v_rx, v_tx)
        ON CONFLICT (tenant_id, router_id, usage_day) DO UPDATE
        SET rx_bytes=router_wan_daily.rx_bytes + EXCLUDED.rx_bytes,
            tx_bytes=router_wan_daily.tx_bytes + EXCLUDED.tx_bytes;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER router_wan_sample_insert AFTER INSERT ON router_telemetry_samples
FOR EACH ROW EXECUTE FUNCTION router_wan_sample_insert();
REVOKE ALL ON FUNCTION router_wan_sample_insert() FROM PUBLIC;

COMMIT;
