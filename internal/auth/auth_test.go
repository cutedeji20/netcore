package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
)

const testTenantID = "11111111-1111-4111-8111-111111111111"

type memoryStore struct {
	tenantID    string
	user        User
	userFound   bool
	tokenHash   []byte
	invalidated bool
	permissions map[string]struct{}
	rehashed    string
}

func (s *memoryStore) ResolveTenant(_ context.Context, slug string) (string, bool, error) {
	return s.tenantID, slug == "example", nil
}

func (s *memoryStore) FindUser(_ context.Context, tenantID, identifier string) (User, bool, error) {
	if tenantID != s.tenantID || identifier != "admin@example.com" {
		return User{}, false, nil
	}
	return s.user, s.userFound, nil
}

func (s *memoryStore) CreateSession(_ context.Context, _ User, tokenHash []byte, _ time.Time, _, _ string) error {
	s.tokenHash = append([]byte(nil), tokenHash...)
	return nil
}

func (s *memoryStore) SessionPrincipal(_ context.Context, tenantID string, tokenHash []byte) (Principal, bool, error) {
	if tenantID != s.tenantID || s.invalidated || string(tokenHash) != string(s.tokenHash) {
		return Principal{}, false, nil
	}
	return Principal{
		SessionID:   "22222222-2222-4222-8222-222222222222",
		TenantID:    s.tenantID,
		UserID:      s.user.ID,
		Email:       s.user.Email,
		Permissions: s.permissions,
	}, true, nil
}

func (s *memoryStore) InvalidateSession(_ context.Context, tenantID string, tokenHash []byte) error {
	if tenantID == s.tenantID && string(tokenHash) == string(s.tokenHash) {
		s.invalidated = true
	}
	return nil
}

func (s *memoryStore) UpdatePasswordHash(_ context.Context, _, _ string, passwordHash string) error {
	s.rehashed = passwordHash
	return nil
}

func newTestService(t *testing.T) (*Service, *memoryStore) {
	t.Helper()
	hasher, err := argon2id.New(argon2id.Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hasher.Hash("correct password")
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{
		tenantID: testTenantID,
		user: User{
			ID:           "33333333-3333-4333-8333-333333333333",
			TenantID:     testTenantID,
			Email:        "admin@example.com",
			PasswordHash: hash,
			Status:       "ACTIVE",
		},
		userFound:   true,
		permissions: map[string]struct{}{"customer.read": {}},
	}
	service, err := NewService(store, hasher, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC) }
	return service, store
}

func TestLoginCreatesHashedSession(t *testing.T) {
	service, store := newTestService(t)
	session, principal, err := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "ADMIN@example.com", Password: "correct password",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if session.ExpiresAt != service.now().Add(time.Hour) {
		t.Fatalf("expiry = %v", session.ExpiresAt)
	}
	if len(store.tokenHash) != 32 {
		t.Fatalf("stored digest length = %d, want 32", len(store.tokenHash))
	}
	if string(store.tokenHash) == session.Token {
		t.Fatal("raw session token was persisted")
	}
	if principal.TenantID != testTenantID || !principal.HasPermission("customer.read") {
		t.Fatalf("unexpected principal: %+v", principal)
	}
}

func TestLoginInvalidCredentialsAreIndistinguishable(t *testing.T) {
	service, store := newTestService(t)
	_, _, wrongPassword := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "admin@example.com", Password: "wrong",
	})
	store.userFound = false
	_, _, unknownUser := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "missing@example.com", Password: "wrong",
	})
	if !errors.Is(wrongPassword, ErrInvalidCredentials) || !errors.Is(unknownUser, ErrInvalidCredentials) {
		t.Fatalf("wrong=%v unknown=%v; both must be ErrInvalidCredentials", wrongPassword, unknownUser)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	service, _ := newTestService(t)
	session, _, err := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "admin@example.com", Password: "correct password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(context.Background(), session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), session.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate after logout = %v, want ErrUnauthenticated", err)
	}
}

func TestSplitSessionTokenRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "bad.token", testTenantID + ".short"} {
		if _, _, err := splitSessionToken(value); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("splitSessionToken(%q) error=%v", value, err)
		}
	}
}
