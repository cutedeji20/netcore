BEGIN;

DELETE FROM role_permissions
 WHERE permission_id = (SELECT id FROM permissions WHERE name = 'auth.mfa_required');

DELETE FROM permissions
 WHERE name = 'auth.mfa_required';

COMMIT;
