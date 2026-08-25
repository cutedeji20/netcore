package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/team"
	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
	"github.com/netcore-isp/netcore/pkg/crypto/totp"
)

type memoryStore struct {
	record Record
	err    error
}

func (s *memoryStore) BootstrapFirstAdmin(_ context.Context, record Record) (Result, error) {
	s.record = record
	return Result{TenantID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222"}, s.err
}

type memorySecrets map[string]string

func (s memorySecrets) Resolve(_ context.Context, reference string) (string, error) {
	v, ok := s[reference]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func newTestService(t *testing.T) (*Service, *memoryStore, string) {
	t.Helper()
	hasher, err := argon2id.New(argon2id.Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{}
	service, err := NewService(store, hasher, memorySecrets{"auth.mfa.initial_admin": secret})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) }
	return service, store, secret
}

func TestRunCreatesOnlyMFAVerifiedFirstAdministrator(t *testing.T) {
	service, store, secret := newTestService(t)
	code, err := totp.Code(secret, service.now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background(), Input{
		TenantName: "NetCore Lagos", TenantSlug: "netcore-lagos", Timezone: "Africa/Lagos", Currency: "ngn",
		Email: "admin@example.com", Password: "correct horse battery staple", TOTPSecretRef: "auth.mfa.initial_admin", TOTPCode: code,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.UserID == "" || store.record.PasswordHash == "" || store.record.InitialTOTPCounter < 0 {
		t.Fatalf("bootstrap result=%+v record=%+v", result, store.record)
	}
	if store.record.Currency != "NGN" || store.record.Email != "admin@example.com" {
		t.Fatalf("record was not normalised: %+v", store.record)
	}
	if len(store.record.InitialRoles) != 4 {
		t.Fatalf("initial roles = %v, want four fixed roles", store.record.InitialRoles)
	}
	if len(store.record.InitialRoleAssignments) != 1 || store.record.InitialRoleAssignments[0] != team.RoleAdministrator {
		t.Fatalf("initial role assignments = %v, want only Administrator", store.record.InitialRoleAssignments)
	}
}

func TestRunRejectsBadAuthenticatorCodeBeforeStore(t *testing.T) {
	service, store, _ := newTestService(t)
	_, err := service.Run(context.Background(), Input{
		TenantName: "NetCore Lagos", TenantSlug: "netcore-lagos", Timezone: "Africa/Lagos", Currency: "NGN",
		Email: "admin@example.com", Password: "correct horse battery staple", TOTPSecretRef: "auth.mfa.initial_admin", TOTPCode: "000000",
	})
	if !errors.Is(err, ErrInvalidInput) || store.record.PasswordHash != "" {
		t.Fatalf("bad code error=%v record=%+v", err, store.record)
	}
}
