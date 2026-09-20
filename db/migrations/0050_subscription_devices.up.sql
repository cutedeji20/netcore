BEGIN;

ALTER TABLE customers
    ADD CONSTRAINT customers_tenant_id_id_key UNIQUE (tenant_id, id);

CREATE TABLE customer_devices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    customer_id uuid NOT NULL,
    normalized_mac text NOT NULL CHECK (normalized_mac ~ '^[0-9a-f]{12}$'),
    label text CHECK (label IS NULL OR char_length(label) <= 120),
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'REMOVED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, normalized_mac),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id) ON DELETE RESTRICT
);

CREATE INDEX customer_devices_customer_idx ON customer_devices (tenant_id, customer_id, created_at DESC);

ALTER TABLE customer_devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_devices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_devices
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_devices TO netcore_app_rw;

ALTER TABLE subscriptions
    ADD COLUMN device_id uuid;
ALTER TABLE subscriptions
    ADD CONSTRAINT subscriptions_device_tenant_fk
    FOREIGN KEY (tenant_id, device_id) REFERENCES customer_devices(tenant_id, id) ON DELETE RESTRICT;
CREATE INDEX subscriptions_device_idx ON subscriptions (tenant_id, device_id) WHERE device_id IS NOT NULL;

COMMIT;
