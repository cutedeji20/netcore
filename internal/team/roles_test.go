package team

import "testing"

func TestBuiltInRolePermissions(t *testing.T) {
	permissions := []string{
		"auth.mfa_required",
		"customer.read", "customer.write",
		"subscription.read", "subscription.write",
		"plan.read", "plan.write",
		"session.read", "session.write",
		"billing.read", "billing.write",
		"network.read", "network.write",
		"voucher.read", "voucher.write",
		"team.read", "team.write",
		"security.read",
		"automation.read", "automation.write",
		"workspace.read", "workspace.write",
		"integration.read", "integration.write",
	}
	wants := map[BuiltInRole]map[string]bool{
		RoleAdministrator: allPermissions(permissions),
		RoleOperations: allowedPermissions(
			"auth.mfa_required",
			"customer.read", "customer.write",
			"subscription.read", "subscription.write",
			"plan.read", "plan.write",
			"session.read", "session.write",
			"network.read", "network.write",
			"voucher.read", "voucher.write",
		),
		RoleBilling: allowedPermissions(
			"auth.mfa_required",
			"customer.read", "customer.write",
			"subscription.read", "subscription.write",
			"billing.read", "billing.write",
			"voucher.read", "voucher.write",
		),
		RoleSupport: allowedPermissions(
			"auth.mfa_required",
			"customer.read", "customer.write",
			"subscription.read", "session.read",
		),
	}

	for _, role := range BuiltInRoles() {
		for _, permission := range permissions {
			want := wants[role][permission]
			if got := role.Allows(permission); got != want {
				t.Errorf("%s Allows(%q) = %t, want %t", role, permission, got, want)
			}
		}
	}
}

func allPermissions(permissions []string) map[string]bool {
	allowed := make(map[string]bool, len(permissions))
	for _, permission := range permissions {
		allowed[permission] = true
	}
	return allowed
}

func allowedPermissions(permissions ...string) map[string]bool {
	allowed := make(map[string]bool, len(permissions))
	for _, permission := range permissions {
		allowed[permission] = true
	}
	return allowed
}
