BEGIN;

DELETE FROM role_permissions AS assignment
 USING permissions AS permission
 WHERE assignment.permission_id = permission.id
   AND permission.name = 'customer.pos_device.write';

DELETE FROM permissions
 WHERE name = 'customer.pos_device.write';

COMMIT;
