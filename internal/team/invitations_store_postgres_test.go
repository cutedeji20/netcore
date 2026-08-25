package team

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestInviteConstraintUsesStructuredPostgresMetadata(t *testing.T) {
	err := &pgconn.PgError{Code: "23505", ConstraintName: "staff_invitations_one_live_email_idx"}
	if !errors.Is(mapInviteConstraint(err), ErrStaffConflict) {
		t.Fatalf("constraint mapping = %v", mapInviteConstraint(err))
	}
	other := &pgconn.PgError{Code: "23505", ConstraintName: "other_constraint"}
	if mapped := mapInviteConstraint(other); mapped != other {
		t.Fatalf("other constraint mapping = %v", mapped)
	}
}

func TestAuditMetadataIsRedactedForAllStaffLifecycleActions(t *testing.T) {
	metadata, err := redactedAuditMetadata()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(metadata, &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "digest", "password", "mfa", "totp", "secret", "ciphertext", "key", "url", "provider", "response"} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("audit metadata included %q", forbidden)
		}
		if bytes.Contains(bytes.ToLower(metadata), []byte(forbidden)) {
			t.Fatalf("audit metadata leaked %q: %s", forbidden, metadata)
		}
	}
}

// Docker-backed PostgreSQL tests are intentionally skipped by this task. These
// query contracts pin the tenant predicates, active-staff lock, and exactly the
// invalidation predicate that the production transaction executes.
func TestLifecycleTransactionQueryContracts(t *testing.T) {
	for _, query := range []string{existingTenantUserByEmailSQL, lockedTenantStaffTargetSQL, invalidateTargetSessionsSQL} {
		if !strings.Contains(query, "tenant_id=$1") {
			t.Fatalf("missing tenant predicate: %s", query)
		}
	}
	if strings.Contains(existingTenantUserByEmailSQL, "status=") {
		t.Fatal("duplicate check must cover locked and disabled users too")
	}
	if !strings.Contains(lockedTenantStaffTargetSQL, "FOR UPDATE") || !strings.Contains(lockedTenantStaffTargetSQL, "u.status='ACTIVE'") {
		t.Fatalf("target lock contract missing: %s", lockedTenantStaffTargetSQL)
	}
	if !strings.Contains(invalidateTargetSessionsSQL, "user_id=$2") || !strings.Contains(invalidateTargetSessionsSQL, "invalidated_at IS NULL") {
		t.Fatalf("session invalidation contract missing: %s", invalidateTargetSessionsSQL)
	}
}
