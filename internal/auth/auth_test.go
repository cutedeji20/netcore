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
	tenantID         string
	user             User
	userFound        bool
	tokenHash        []byte
	invalidated      bool
	permissions      map[string]struct{}
	rehashed         string
	sessionCreatedAt time.Time
	sessionExpiresAt time.Time
}

type testMFAVerifier struct {
	err   error
	calls int
	code  string
}

func (v *testMFAVerifier) VerifyTOTP(_ context.Context, tenantID, userID, code string) error {
	if tenantID != testTenantID || userID != "33333333-3333-4333-8333-333333333333" {
		return ErrInvalidMFA
	}
	v.calls++
	v.code = code
	return v.err
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

func (s *memoryStore) CreateSession(_ context.Context, _ User, tokenHash []byte, expiresAt time.Time, _, _ string) error {
	s.tokenHash = append([]byte(nil), tokenHash...)
	s.sessionCreatedAt = expiresAt.Add(-time.Hour)
	s.sessionExpiresAt = expiresAt
	return nil
}

func (s *memoryStore) SessionPrincipal(_ context.Context, tenantID string, tokenHash []byte) (Principal, bool, error) {
	if tenantID != s.tenantID || s.invalidated || string(tokenHash) != string(s.tokenHash) {
		return Principal{}, false, nil
	}
	principal := Principal{
		SessionID:        "22222222-2222-4222-8222-222222222222",
		SessionCreatedAt: s.sessionCreatedAt,
		SessionExpiresAt: s.sessionExpiresAt,
		TenantID:         s.tenantID,
		UserID:           s.user.ID,
		Email:            s.user.Email,
		Permissions:      s.permissions,
	}
	return principal, true, nil
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
			RequiresMFA:  true,
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

func TestLoginRequiresMFABeforeCreatingSession(t *testing.T) {
	service, store := newTestService(t)
	verifier := &testMFAVerifier{err: ErrInvalidMFA}
	if err := service.RequireMFA(verifier); err != nil {
		t.Fatal(err)
	}
	_, _, err := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "admin@example.com", Password: "correct password", MFACode: "123456",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalid MFA error = %v, want invalid credentials", err)
	}
	if verifier.calls != 1 || verifier.code != "123456" {
		t.Fatalf("MFA verifier calls=%d code=%q", verifier.calls, verifier.code)
	}
	if len(store.tokenHash) != 0 {
		t.Fatal("session was created before MFA succeeded")
	}

	verifier.err = nil
	if _, _, err := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "admin@example.com", Password: "correct password", MFACode: "654321",
	}); err != nil {
		t.Fatalf("MFA-backed login: %v", err)
	}
	if verifier.calls != 2 || verifier.code != "654321" || len(store.tokenHash) != 32 {
		t.Fatalf("successful MFA login calls=%d code=%q token_hash=%d", verifier.calls, verifier.code, len(store.tokenHash))
	}
}

func TestLoginDoesNotRequireTOTPForCustomerWithoutStaffPermission(t *testing.T) {
	service, store := newTestService(t)
	store.user.RequiresMFA = false
	store.user.EmailVerified = true
	verifier := &testMFAVerifier{err: ErrInvalidMFA}
	if err := service.RequireMFA(verifier); err != nil {
		t.Fatal(err)
	}

	if _, _, err := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "admin@example.com", Password: "correct password",
	}); err != nil {
		t.Fatalf("customer login = %v", err)
	}
	if verifier.calls != 0 {
		t.Fatalf("customer login called TOTP verifier %d times", verifier.calls)
	}
	if len(store.tokenHash) != 32 {
		t.Fatal("customer session was not created")
	}
}

func TestLoginRejectsUnverifiedCustomerBeforeCreatingSession(t *testing.T) {
	service, store := newTestService(t)
	store.user.RequiresMFA = false
	store.user.EmailVerified = false

	_, _, err := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "admin@example.com", Password: "correct password",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unverified customer error = %v, want ErrInvalidCredentials", err)
	}
	if len(store.tokenHash) != 0 {
		t.Fatal("unverified customer created a session")
	}
}

func TestLoginTreatsMFAStoreFailureAsUnavailable(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.RequireMFA(&testMFAVerifier{err: ErrMFAUnavailable}); err != nil {
		t.Fatal(err)
	}
	_, _, err := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "admin@example.com", Password: "correct password", MFACode: "123456",
	})
	if !errors.Is(err, ErrMFAUnavailable) {
		t.Fatalf("MFA dependency failure = %v, want ErrMFAUnavailable", err)
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

func TestAuthenticateRejectsExistingSessionPastConfiguredTTL(t *testing.T) {
	service, store := newTestService(t)
	session, _, err := service.Login(context.Background(), LoginInput{
		TenantSlug: "example", Identifier: "admin@example.com", Password: "correct password",
	})
	if err != nil {
		t.Fatal(err)
	}
	store.sessionCreatedAt = service.now().Add(-time.Hour)
	store.sessionExpiresAt = service.now().Add(23 * time.Hour)

	if _, err := service.Authenticate(context.Background(), session.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate old session = %v, want unauthenticated", err)
	}
}

func TestSplitSessionTokenRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "bad.token", testTenantID + ".short"} {
		if _, _, err := splitSessionToken(value); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("splitSessionToken(%q) error=%v", value, err)
		}
	}
}
