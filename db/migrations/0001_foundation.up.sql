-- 0001_foundation.up.sql
-- NetCore ISP Platform — Phase 1 foundation schema.
-- Spec: BUILD.md v1.1 §8, §9, §9A, §10, §60.1
--
-- Every constraint in this file exists because its absence produces a specific,
-- verified failure. Where that is non-obvious the comment names the failure.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;   -- case-insensitive email; prevents
                                         -- Alice@x.com / alice@x.com duplicate accounts

-- ---------------------------------------------------------------------------
-- §9 tenants
-- ---------------------------------------------------------------------------
CREATE TABLE tenants (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text        NOT NULL,
    slug        text        NOT NULL UNIQUE,
    status      text        NOT NULL DEFAULT 'ACTIVE'
                            CHECK (status IN ('ACTIVE','SUSPENDED','CLOSED')),
    timezone    text        NOT NULL DEFAULT 'UTC',
    currency    char(3)     NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- §9 users / §40 RBAC
-- ---------------------------------------------------------------------------
CREATE TABLE users (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    email             citext,
    phone             text,
    password_hash     text        NOT NULL,
    password_params   jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- §11 rehash-on-login
    status            text        NOT NULL DEFAULT 'ACTIVE'
                                  CHECK (status IN ('ACTIVE','LOCKED','DISABLED')),
    email_verified_at timestamptz,
    phone_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_identity_present CHECK (email IS NOT NULL OR phone IS NOT NULL)
);
CREATE UNIQUE INDEX users_tenant_email_key ON users (tenant_id, email) WHERE email IS NOT NULL;
CREATE UNIQUE INDEX users_tenant_phone_key ON users (tenant_id, phone) WHERE phone IS NOT NULL;

CREATE TABLE roles (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name      text NOT NULL,
    UNIQUE (tenant_id, name)
);

CREATE TABLE permissions (
    id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE
);

CREATE TABLE role_permissions (
    role_id       uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE user_roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

-- ---------------------------------------------------------------------------
-- §9 customers
-- ---------------------------------------------------------------------------
CREATE TABLE customers (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    user_id         uuid        REFERENCES users(id) ON DELETE SET NULL,
    customer_number text        NOT NULL,
    status          text        NOT NULL DEFAULT 'ACTIVE'
                                CHECK (status IN ('ACTIVE','SUSPENDED','CLOSED')),
    first_name      text,
    last_name       text,
    phone           text,
    email           citext,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, customer_number)
);

-- ---------------------------------------------------------------------------
-- §9 plans — money is integer minor units (§101/§102), quota fields per §21A
-- ---------------------------------------------------------------------------
CREATE TABLE plans (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id              uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    name                   text        NOT NULL,
    description            text,
    price_minor            bigint      NOT NULL CHECK (price_minor >= 0),
    currency               char(3)     NOT NULL,
    duration_seconds       bigint      NOT NULL CHECK (duration_seconds > 0),

    download_bps           bigint      NOT NULL CHECK (download_bps > 0),
    upload_bps             bigint      NOT NULL CHECK (upload_bps > 0),
    burst_download_bps     bigint      CHECK (burst_download_bps IS NULL OR burst_download_bps >= download_bps),
    burst_upload_bps       bigint      CHECK (burst_upload_bps   IS NULL OR burst_upload_bps   >= upload_bps),

    -- §27: these are DIFFERENT numbers. Registered devices vs simultaneously online.
    max_devices            int         NOT NULL DEFAULT 1 CHECK (max_devices > 0),
    max_concurrent_sessions int        NOT NULL DEFAULT 1 CHECK (max_concurrent_sessions > 0),

    -- §21A quota
    quota_bytes            bigint      CHECK (quota_bytes IS NULL OR quota_bytes > 0),  -- NULL = unmetered
    quota_reset_policy     text        NOT NULL DEFAULT 'NONE'
                                       CHECK (quota_reset_policy IN ('NONE','PER_SUBSCRIPTION','DAILY','MONTHLY')),
    quota_exhausted_action text        NOT NULL DEFAULT 'THROTTLE'
                                       CHECK (quota_exhausted_action IN ('DISCONNECT','THROTTLE','REDIRECT')),
    throttle_download_bps  bigint      CHECK (throttle_download_bps IS NULL OR throttle_download_bps > 0),
    throttle_upload_bps    bigint      CHECK (throttle_upload_bps   IS NULL OR throttle_upload_bps   > 0),

    session_timeout_seconds bigint     NOT NULL DEFAULT 14400 CHECK (session_timeout_seconds > 0),
    idle_timeout_seconds    bigint     NOT NULL DEFAULT 600   CHECK (idle_timeout_seconds > 0),
    priority                int        NOT NULL DEFAULT 8,
    status                  text       NOT NULL DEFAULT 'ACTIVE'
                                       CHECK (status IN ('ACTIVE','RETIRED')),
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),

    -- A THROTTLE plan without throttle speeds silently grants full rate on exhaustion.
    CONSTRAINT plans_throttle_configured CHECK (
        quota_exhausted_action <> 'THROTTLE'
        OR (throttle_download_bps IS NOT NULL AND throttle_upload_bps IS NOT NULL)
    ),
    -- An unmetered plan with a reset policy is a contradiction that would create
    -- counter rows nothing ever reads.
    CONSTRAINT plans_quota_policy_coherent CHECK (
        quota_bytes IS NOT NULL OR quota_reset_policy = 'NONE'
    )
);

-- ---------------------------------------------------------------------------
-- §9 subscriptions / §20 state machine
-- ---------------------------------------------------------------------------
CREATE TABLE subscriptions (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    customer_id    uuid        NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    plan_id        uuid        NOT NULL REFERENCES plans(id) ON DELETE RESTRICT,
    status         text        NOT NULL DEFAULT 'PENDING'
                               CHECK (status IN ('PENDING','ACTIVE','SUSPENDED','EXPIRED','CANCELLED')),
    starts_at      timestamptz,
    expires_at     timestamptz,
    auto_renew     boolean     NOT NULL DEFAULT false,
    payment_status text        NOT NULL DEFAULT 'UNPAID'
                               CHECK (payment_status IN ('UNPAID','PAID','REFUNDED','PARTIAL')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT subs_period_ordered CHECK (expires_at IS NULL OR starts_at IS NULL OR expires_at > starts_at),
    -- An ACTIVE subscription with no expiry never expires. §20 + §23 both depend
    -- on expires_at to bound Session-Timeout.
    CONSTRAINT subs_active_has_period CHECK (
        status <> 'ACTIVE' OR (starts_at IS NOT NULL AND expires_at IS NOT NULL)
    )
);
CREATE INDEX subscriptions_expiry_idx ON subscriptions (expires_at)
    WHERE status = 'ACTIVE';
CREATE INDEX subscriptions_customer_idx ON subscriptions (tenant_id, customer_id);

-- §9A subscription_events — append-only history. §20.
CREATE TABLE subscription_events (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    subscription_id uuid        NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    from_status     text,
    to_status       text        NOT NULL,
    reason          text        NOT NULL,
    actor_type      text        NOT NULL CHECK (actor_type IN ('SYSTEM','ADMIN','CUSTOMER','GATEWAY')),
    actor_id        uuid,
    metadata        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX subscription_events_sub_idx ON subscription_events (subscription_id, created_at);

-- ---------------------------------------------------------------------------
-- §9 devices — §26 MAC normalization
-- ---------------------------------------------------------------------------
CREATE TABLE devices (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    customer_id   uuid        NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    mac_address   text,
    normalized_mac text,
    hostname      text,
    device_type   text,
    last_ip       inet,
    last_seen_at  timestamptz,
    status        text        NOT NULL DEFAULT 'ACTIVE'
                              CHECK (status IN ('ACTIVE','BLOCKED','REMOVED')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    -- §26: canonical form is lowercase hex, no separators, exactly 12 chars.
    CONSTRAINT devices_mac_canonical CHECK (
        normalized_mac IS NULL OR normalized_mac ~ '^[0-9a-f]{12}$'
    )
);
CREATE UNIQUE INDEX devices_tenant_mac_key ON devices (tenant_id, normalized_mac)
    WHERE normalized_mac IS NOT NULL AND status <> 'REMOVED';

-- ---------------------------------------------------------------------------
-- §9 sites / routers / access_points
-- ---------------------------------------------------------------------------
CREATE TABLE sites (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    name      text NOT NULL,
    code      text NOT NULL,
    address   text,
    latitude  numeric(9,6),
    longitude numeric(9,6),
    status    text NOT NULL DEFAULT 'ACTIVE',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, code)
);

CREATE TABLE routers (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    site_id           uuid        REFERENCES sites(id) ON DELETE SET NULL,
    name              text        NOT NULL,
    serial_number     text,
    management_ip     inet        NOT NULL,
    api_endpoint      text,
    status            text        NOT NULL DEFAULT 'PROVISIONING'
                                  CHECK (status IN ('PROVISIONING','ONLINE','OFFLINE','RETIRED')),
    routeros_version  text,
    last_seen_at      timestamptz,
    -- §33: references only. A password in either of these columns is an incident.
    credential_ref    text        NOT NULL,
    radius_secret_ref text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name),
    -- §17/§32: management addresses must be private. Enforced here so a
    -- misconfigured router row cannot become an SSRF target.
    CONSTRAINT routers_management_ip_private CHECK (
        management_ip << inet '10.0.0.0/8'      OR
        management_ip << inet '172.16.0.0/12'   OR
        management_ip << inet '192.168.0.0/16'  OR
        management_ip << inet 'fd00::/8'
    ),
    -- A secret VALUE in a *_ref column is the failure mode this guards.
    -- Refs are paths; they contain '/' and never whitespace.
    CONSTRAINT routers_credential_ref_is_path CHECK (credential_ref    ~ '^[a-z0-9/_.-]+$'),
    CONSTRAINT routers_radius_ref_is_path     CHECK (radius_secret_ref ~ '^[a-z0-9/_.-]+$')
);

CREATE TABLE access_points (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    site_id       uuid REFERENCES sites(id) ON DELETE SET NULL,
    router_id     uuid REFERENCES routers(id) ON DELETE SET NULL,
    name          text NOT NULL,
    mac_address   text,
    management_ip inet,
    model         text,
    vendor        text,
    status        text NOT NULL DEFAULT 'UNKNOWN',
    last_seen_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- §9A nas — registered RADIUS clients.
-- FreeRADIUS reads this to decide which NAS may speak to it at all (§22).
-- ---------------------------------------------------------------------------
CREATE TABLE nas (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    router_id   uuid        REFERENCES routers(id) ON DELETE CASCADE,
    nasname     inet        NOT NULL UNIQUE,
    shortname   text        NOT NULL,
    type        text        NOT NULL DEFAULT 'mikrotik',
    ports       int,
    secret_ref  text        NOT NULL,     -- §33 reference, never the shared secret
    server      text,
    community   text,
    description text,
    coa_port    int         NOT NULL DEFAULT 3799 CHECK (coa_port BETWEEN 1 AND 65535),
    status      text        NOT NULL DEFAULT 'ACTIVE'
                            CHECK (status IN ('ACTIVE','DISABLED')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT nas_secret_ref_is_path CHECK (secret_ref ~ '^[a-z0-9/_.-]+$')
);

COMMIT;
