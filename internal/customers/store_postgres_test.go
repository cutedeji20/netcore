package customers

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestCustomerWriteMapsEmailUniqueViolationToTypedConflict(t *testing.T) {
	err := customerWriteError("insert customer", &pgconn.PgError{Code: "23505", ConstraintName: "customers_tenant_email_key"})
	if !errors.Is(err, ErrDuplicateEmail) {
		t.Fatalf("error = %v, want ErrDuplicateEmail", err)
	}
}

func TestCustomerWriteDoesNotMisclassifyOtherDatabaseFailures(t *testing.T) {
	original := &pgconn.PgError{Code: "23505", ConstraintName: "customers_tenant_user_id_key"}
	err := customerWriteError("insert customer", original)
	if errors.Is(err, ErrDuplicateEmail) || !errors.Is(err, original) {
		t.Fatalf("error = %v", err)
	}
}

func TestCustomerMutationQueriesKeepTenantLifecycleAndAuditContracts(t *testing.T) {
	for name, query := range map[string]string{
		"create":     customerCreateSQL,
		"update":     customerUpdateSQL,
		"deactivate": customerDeactivateSQL,
	} {
		if !strings.Contains(query, "tenant_id") {
			t.Fatalf("%s query lost tenant scoping: %s", name, query)
		}
	}
	if !strings.Contains(customerUpdateSQL, "WHERE tenant_id = $1 AND id = $2::uuid") || !strings.Contains(customerDeactivateSQL, "WHERE tenant_id = $1 AND id = $2::uuid") {
		t.Fatal("customer target mutations must retain an explicit tenant predicate")
	}
	if !strings.Contains(customerDeactivateSQL, "SET status = 'SUSPENDED', updated_at = now()") || strings.Contains(customerDeactivateSQL, "subscriptions") || strings.Contains(customerDeactivateSQL, "router") {
		t.Fatalf("deactivation must only suspend the customer: %s", customerDeactivateSQL)
	}
	if !strings.Contains(customerAuditSQL, "'{}'::jsonb") || strings.Contains(customerAuditSQL, "email") || strings.Contains(customerAuditSQL, "first_name") || strings.Contains(customerAuditSQL, "password") {
		t.Fatalf("customer audit query is not redacted: %s", customerAuditSQL)
	}
}

func TestUpdateDuplicateViolationUsesTheSameTypedConflict(t *testing.T) {
	err := customerWriteError("update customer", &pgconn.PgError{Code: "23505", ConstraintName: "customers_tenant_email_key"})
	if !errors.Is(err, ErrDuplicateEmail) {
		t.Fatalf("error = %v, want ErrDuplicateEmail", err)
	}
}
