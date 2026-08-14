-- 0003_payments_audit_outbox.up.sql
-- Spec: BUILD.md §9, §9A, §18, §19, §38, §53, §97
-- Money, idempotency, audit. Everything here is append-only or uniquely keyed.

BEGIN;

-- ---------------------------------------------------------------------------
-- §9 payments — §18: amount is FROZEN server-side at creation.
-- ---------------------------------------------------------------------------
CREATE TABLE payments (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    customer_id        uuid        NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    subscription_id    uuid        REFERENCES subscriptions(id) ON DELETE RESTRICT,
    gateway            text        NOT NULL,
    provider_reference text        NOT NULL,
    amount_minor       bigint      NOT NULL CHECK (amount_minor > 0),
    currency           char(3)     NOT NULL,
    status             text        NOT NULL DEFAULT 'PENDING'
                                   CHECK (status IN ('PENDING','SUCCESS','FAILED','ABANDONED','REFUNDED')),
    verified_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),

    -- §18: the unique constraint IS the idempotency lock for gateway callbacks.
    CONSTRAINT payments_provider_key UNIQUE (gateway, provider_reference),

    -- §18A: SUCCESS without server-to-server verification is exactly the
    -- "activated on browser redirect" bug this spec forbids.
    CONSTRAINT payments_success_verified CHECK (
        status <> 'SUCCESS' OR verified_at IS NOT NULL
    )
);
CREATE INDEX payments_customer_idx ON payments (tenant_id, customer_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- §9 invoices
-- ---------------------------------------------------------------------------
CREATE TABLE invoices (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    customer_id     uuid        NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    subscription_id uuid        REFERENCES subscriptions(id) ON DELETE RESTRICT,
    invoice_number  text        NOT NULL,
    amount_minor    bigint      NOT NULL CHECK (amount_minor >= 0),
    currency        char(3)     NOT NULL,
    status          text        NOT NULL DEFAULT 'DRAFT'
                                CHECK (status IN ('DRAFT','ISSUED','PAID','VOID')),
    issued_at       timestamptz,
    due_at          timestamptz,
    paid_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, invoice_number),
    CONSTRAINT invoices_issued_has_date CHECK (status = 'DRAFT' OR issued_at IS NOT NULL),
    CONSTRAINT invoices_paid_has_date   CHECK (status <> 'PAID' OR paid_at IS NOT NULL)
);

-- ---------------------------------------------------------------------------
-- §9 vouchers — §86: hashes only, atomic redemption.
-- ---------------------------------------------------------------------------
CREATE TABLE vouchers (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    code_hash   bytea       NOT NULL,
    code_prefix text        NOT NULL,        -- first 4 chars, for support lookup
    plan_id     uuid        NOT NULL REFERENCES plans(id) ON DELETE RESTRICT,
    reseller_id uuid,
    site_id     uuid        REFERENCES sites(id) ON DELETE SET NULL,
    batch_id    uuid,
    status      text        NOT NULL DEFAULT 'UNUSED'
                            CHECK (status IN ('UNUSED','REDEEMED','EXPIRED','REVOKED')),
    expires_at  timestamptz,
    redeemed_at timestamptz,
    redeemed_by uuid        REFERENCES customers(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, code_hash),
    CONSTRAINT vouchers_redeemed_complete CHECK (
        (status = 'REDEEMED') = (redeemed_at IS NOT NULL)
    ),
    -- SHA-256 is 32 bytes. A shorter value means someone stored something else.
    CONSTRAINT vouchers_hash_len CHECK (octet_length(code_hash) = 32)
);
CREATE INDEX vouchers_prefix_idx ON vouchers (tenant_id, code_prefix);
CREATE INDEX vouchers_batch_idx  ON vouchers (batch_id) WHERE batch_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- §9A webhook_events — §19 idempotency ledger.
-- The unique index IS the lock. INSERT ... ON CONFLICT DO NOTHING RETURNING id;
-- an empty RETURNING means another instance owns this event.
-- ---------------------------------------------------------------------------
CREATE TABLE webhook_events (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    provider        text        NOT NULL,
    event_id        text        NOT NULL,
    event_type      text,
    payload_hash    bytea       NOT NULL,
    signature_valid boolean     NOT NULL,
    received_at     timestamptz NOT NULL DEFAULT now(),
    processed_at    timestamptz,
    status          text        NOT NULL DEFAULT 'RECEIVED'
                                CHECK (status IN ('RECEIVED','PROCESSED','FAILED','IGNORED')),
    attempts        int         NOT NULL DEFAULT 0,
    last_error      text,
    UNIQUE (provider, event_id)
);
CREATE INDEX webhook_events_unprocessed_idx
    ON webhook_events (received_at) WHERE processed_at IS NULL;

-- ---------------------------------------------------------------------------
-- §9A outbox_events — §53 transactional outbox.
-- ---------------------------------------------------------------------------
CREATE TABLE outbox_events (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       uuid        NOT NULL UNIQUE,
    tenant_id      uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    aggregate_type text        NOT NULL,
    aggregate_id   uuid        NOT NULL,
    event_type     text        NOT NULL,
    payload        jsonb       NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    published_at   timestamptz,
    attempts       int         NOT NULL DEFAULT 0
);
-- The publisher only ever scans unpublished rows.
CREATE INDEX outbox_unpublished_idx
    ON outbox_events (created_at) WHERE published_at IS NULL;

-- ---------------------------------------------------------------------------
-- §9A idempotency_keys — §97
-- ---------------------------------------------------------------------------
CREATE TABLE idempotency_keys (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id         uuid,
    endpoint        text        NOT NULL,
    key             text        NOT NULL,
    request_hash    bytea       NOT NULL,
    response_status int,
    response_body   jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    UNIQUE (tenant_id, key, endpoint)
);
CREATE INDEX idempotency_expiry_idx ON idempotency_keys (expires_at);

-- ---------------------------------------------------------------------------
-- §38 double-entry ledger. Balance is DERIVED, never a mutable column.
-- ---------------------------------------------------------------------------
CREATE TABLE ledger_accounts (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    owner_type   text        NOT NULL CHECK (owner_type IN ('CUSTOMER','RESELLER','PLATFORM')),
    owner_id     uuid,
    account_type text        NOT NULL CHECK (account_type IN ('ASSET','LIABILITY','REVENUE','EXPENSE','EQUITY')),
    currency     char(3)     NOT NULL,
    status       text        NOT NULL DEFAULT 'ACTIVE',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ledger_transactions (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    reference   text,
    description text,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ledger_entries (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    transaction_id uuid        NOT NULL REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    account_id     uuid        NOT NULL REFERENCES ledger_accounts(id) ON DELETE RESTRICT,
    direction      text        NOT NULL CHECK (direction IN ('DEBIT','CREDIT')),
    amount_minor   bigint      NOT NULL CHECK (amount_minor > 0),
    currency       char(3)     NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ledger_entries_txn_idx     ON ledger_entries (transaction_id);
CREATE INDEX ledger_entries_account_idx ON ledger_entries (account_id, created_at);

-- §38: every transaction must balance. Enforced by the DATABASE, not by Go,
-- and DEFERRED so entries can be inserted one at a time within a transaction.
CREATE FUNCTION ledger_assert_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_txn uuid := COALESCE(NEW.transaction_id, OLD.transaction_id);
    v_debit  bigint;
    v_credit bigint;
BEGIN
    SELECT COALESCE(SUM(amount_minor) FILTER (WHERE direction = 'DEBIT'), 0),
           COALESCE(SUM(amount_minor) FILTER (WHERE direction = 'CREDIT'), 0)
      INTO v_debit, v_credit
      FROM ledger_entries WHERE transaction_id = v_txn;

    IF v_debit <> v_credit THEN
        RAISE EXCEPTION
            'ledger transaction % does not balance: debits=% credits=%',
            v_txn, v_debit, v_credit USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER ledger_entries_balanced
    AFTER INSERT OR UPDATE OR DELETE ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION ledger_assert_balanced();

-- ---------------------------------------------------------------------------
-- §9A / §39 audit_logs — append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE audit_logs (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid,
    actor_type    text        NOT NULL,
    actor_id      uuid,
    action        text        NOT NULL,
    resource_type text,
    resource_id   uuid,
    ip_address    inet,
    user_agent    text,
    request_id    text,
    metadata      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_tenant_idx   ON audit_logs (tenant_id, created_at DESC);
CREATE INDEX audit_logs_actor_idx    ON audit_logs (actor_id, created_at DESC);
CREATE INDEX audit_logs_resource_idx ON audit_logs (resource_type, resource_id, created_at DESC);

-- §39: "an audit log the application can rewrite is not an audit log."
-- Grants alone are not enough — a trigger makes the intent explicit and
-- survives a careless future GRANT.
CREATE FUNCTION audit_logs_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only (BUILD.md §39): % denied', TG_OP
        USING ERRCODE = '42501';
END;
$$;

CREATE TRIGGER audit_logs_no_update BEFORE UPDATE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();
CREATE TRIGGER audit_logs_no_delete BEFORE DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();

CREATE TRIGGER subscription_events_no_update BEFORE UPDATE ON subscription_events
    FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();
CREATE TRIGGER subscription_events_no_delete BEFORE DELETE ON subscription_events
    FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();

COMMIT;
