-- 0031_staff_mfa_permission.up.sql
-- Privileged staff sessions require TOTP, while verified customer accounts do
-- not inherit an administrator MFA requirement merely by signing in.

BEGIN;

INSERT INTO permissions (name)
VALUES ('auth.mfa_required')
ON CONFLICT (name) DO NOTHING;

-- Existing roles that already have at least one operational permission are
-- staff roles. Preserve the current production administrator's MFA boundary
-- when moving from the prior all-user policy to the explicit policy.
INSERT INTO role_permissions (role_id, permission_id)
SELECT DISTINCT existing.role_id, required.id
  FROM role_permissions AS existing
 CROSS JOIN (SELECT id FROM permissions WHERE name = 'auth.mfa_required') AS required
ON CONFLICT (role_id, permission_id) DO NOTHING;

COMMIT;
