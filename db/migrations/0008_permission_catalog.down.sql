BEGIN;

DELETE FROM permissions
 WHERE name IN (
    'customer.read',
    'customer.write',
    'subscription.read',
    'subscription.write'
 );

COMMIT;
