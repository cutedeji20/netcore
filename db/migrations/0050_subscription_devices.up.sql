BEGIN;

-- `devices` is the existing RADIUS identity source. Reusing it means the
-- customer chooses the same record that RADIUS resolves for Calling-Station-Id;
-- a parallel table would allow contradictory ownership for one MAC.
ALTER TABLE devices
    ADD CONSTRAINT devices_tenant_id_id_key UNIQUE (tenant_id, id);

ALTER TABLE subscriptions
    ADD COLUMN device_id uuid;
ALTER TABLE subscriptions
    ADD CONSTRAINT subscriptions_device_tenant_fk
    FOREIGN KEY (tenant_id, device_id) REFERENCES devices(tenant_id, id) ON DELETE RESTRICT;
CREATE INDEX subscriptions_device_idx ON subscriptions (tenant_id, device_id) WHERE device_id IS NOT NULL;

COMMIT;
