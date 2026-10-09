BEGIN;

CREATE TABLE voucher_orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    buyer_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    plan_id uuid NOT NULL REFERENCES plans(id) ON DELETE RESTRICT,
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 100),
    unit_base_minor bigint NOT NULL CHECK (unit_base_minor > 0),
    discount_basis_points integer NOT NULL CHECK (discount_basis_points BETWEEN 0 AND 9999),
    unit_discount_minor bigint NOT NULL CHECK (unit_discount_minor >= 0),
    unit_net_minor bigint NOT NULL CHECK (unit_net_minor >= 0),
    bank_charge_minor bigint NOT NULL CHECK (bank_charge_minor >= 0),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency char(3) NOT NULL,
    reseller_at_initiation boolean NOT NULL DEFAULT false,
    payment_id uuid UNIQUE REFERENCES payments(id) ON DELETE RESTRICT,
    status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PAID','FAILED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    paid_at timestamptz,
    CONSTRAINT voucher_orders_price_check CHECK (
        unit_base_minor = unit_net_minor + unit_discount_minor
        AND amount_minor = unit_net_minor * quantity + bank_charge_minor
        AND (reseller_at_initiation OR (discount_basis_points = 0 AND unit_discount_minor = 0))
        AND (status <> 'PAID' OR (payment_id IS NOT NULL AND paid_at IS NOT NULL))
    )
);
ALTER TABLE voucher_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE voucher_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON voucher_orders USING (tenant_id = current_tenant_id()) WITH CHECK (tenant_id = current_tenant_id());
GRANT SELECT, INSERT, UPDATE ON voucher_orders TO netcore_app_rw;

CREATE TABLE voucher_batches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    plan_id uuid NOT NULL REFERENCES plans(id) ON DELETE RESTRICT,
    order_id uuid UNIQUE REFERENCES voucher_orders(id) ON DELETE RESTRICT,
    buyer_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
    issuer_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 100),
    reason text,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT voucher_batch_owner_check CHECK ((buyer_user_id IS NOT NULL) OR (issuer_user_id IS NOT NULL))
);
ALTER TABLE voucher_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE voucher_batches FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON voucher_batches USING (tenant_id = current_tenant_id()) WITH CHECK (tenant_id = current_tenant_id());
GRANT SELECT, INSERT ON voucher_batches TO netcore_app_rw;

ALTER TABLE vouchers
    ADD COLUMN order_id uuid REFERENCES voucher_orders(id) ON DELETE RESTRICT,
    ADD COLUMN order_position integer CHECK (order_position BETWEEN 1 AND 100),
    ADD COLUMN code_ciphertext bytea,
    ADD COLUMN code_nonce bytea,
    ADD COLUMN code_wrapped_dek bytea,
    ADD COLUMN code_kek_key_id text,
    ADD CONSTRAINT vouchers_code_envelope_complete CHECK (
        (code_ciphertext IS NULL AND code_nonce IS NULL AND code_wrapped_dek IS NULL AND code_kek_key_id IS NULL)
        OR (code_ciphertext IS NOT NULL AND code_nonce IS NOT NULL AND code_wrapped_dek IS NOT NULL AND code_kek_key_id IS NOT NULL)
    ),
    ADD CONSTRAINT vouchers_order_position_complete CHECK ((order_id IS NULL) = (order_position IS NULL));
CREATE UNIQUE INDEX vouchers_order_position_unique ON vouchers (tenant_id, order_id, order_position) WHERE order_id IS NOT NULL;

COMMIT;
