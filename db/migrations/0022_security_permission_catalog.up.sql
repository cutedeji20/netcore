-- Security Center visibility is a distinct staff capability.

BEGIN;

INSERT INTO permissions (name)
VALUES
    ('security.read')
ON CONFLICT (name) DO NOTHING;

COMMIT;
