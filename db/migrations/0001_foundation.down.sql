BEGIN;
DROP TABLE IF EXISTS nas, access_points, routers, sites, devices,
  subscription_events, subscriptions, plans, customers,
  user_roles, role_permissions, permissions, roles, users, tenants CASCADE;
COMMIT;
