package team

// BuiltInRole is one of the fixed staff roles available to every tenant.
type BuiltInRole string

const (
	RoleAdministrator BuiltInRole = "Administrator"
	RoleOperations    BuiltInRole = "Operations"
	RoleBilling       BuiltInRole = "Billing"
	RoleSupport       BuiltInRole = "Support"
)

var rolePermissions = map[BuiltInRole]map[string]struct{}{
	RoleAdministrator: permissionSet(
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
	),
	RoleOperations: permissionSet(
		"auth.mfa_required",
		"customer.read", "customer.write",
		"subscription.read", "subscription.write",
		"plan.read", "plan.write",
		"voucher.read", "voucher.write",
		"network.read", "network.write",
		"session.read", "session.write",
	),
	RoleBilling: permissionSet(
		"auth.mfa_required",
		"customer.read", "customer.write",
		"subscription.read", "subscription.write",
		"billing.read", "billing.write",
		"voucher.read", "voucher.write",
	),
	RoleSupport: permissionSet(
		"auth.mfa_required",
		"customer.read", "customer.write",
		"subscription.read", "session.read",
	),
}

func permissionSet(permissions ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		set[permission] = struct{}{}
	}
	return set
}

// Allows reports whether the fixed role grants a catalogue permission.
func (r BuiltInRole) Allows(permission string) bool {
	_, ok := rolePermissions[r][permission]
	return ok
}

// BuiltInRoles returns the complete canonical staff role set in stable order.
func BuiltInRoles() []BuiltInRole {
	return []BuiltInRole{RoleAdministrator, RoleOperations, RoleBilling, RoleSupport}
}

// Permissions returns a copy of the permissions granted to the fixed role.
// It is used when seeding tenant roles, keeping the bootstrap ceremony aligned
// with the application policy without exposing the policy map for mutation.
func (r BuiltInRole) Permissions() []string {
	permissions := make([]string, 0, len(rolePermissions[r]))
	for _, permission := range []string{
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
	} {
		if r.Allows(permission) {
			permissions = append(permissions, permission)
		}
	}
	return permissions
}
