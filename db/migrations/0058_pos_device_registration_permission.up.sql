BEGIN;

INSERT INTO permissions (name)
VALUES ('customer.pos_device.write')
ON CONFLICT (name) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT role.id, permission.id
  FROM roles AS role
 CROSS JOIN permissions AS permission
 WHERE role.name = 'Administrator'
   AND permission.name = 'customer.pos_device.write'
ON CONFLICT DO NOTHING;

COMMIT;
