// Package auth implements browser-session authentication and explicit
// permission checks. Roles remain storage-only bundles; handlers authorize
// against permissions exclusively.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
)

var (
	// ErrInvalidCredentials is deliberately used for unknown users, bad
	// passwords, inactive accounts and unknown tenants. The HTTP boundary
	// returns one byte-stable response for all of them.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrUnauthenticated    = errors.New("auth: unauthenticated")
)

// MFAVerifier is deliberately smaller than the persistence-facing MFAStore.
// Password verification must complete before the code is consumed, and a
// session must never exist before this verifier accepts the second factor.
type MFAVerifier interface {
	VerifyTOTP(ctx context.Context, tenantID, userID, code string) error
}

// User is the minimum credential record needed by the login service.
type User struct {
	ID            string
	TenantID      string
	Email         string
	PasswordHash  string
	Status        string
	RequiresMFA   bool
	EmailVerified bool
}

// Principal represents an authenticated actor. Permissions are a set because
// role names must never become an authorization mechanism.
type Principal struct {
	SessionID        string
	SessionCreatedAt time.Time
	SessionExpiresAt time.Time
	TenantID         string
	UserID           string
	Email            string
	Permissions      map[string]struct{}
}

// HasPermission reports whether p has the named explicit permission.
func (p Principal) HasPermission(permission string) bool {
	_, ok := p.Permissions[permission]
	return ok
}

// Session is the raw cookie credential returned only at creation time.
// Token must never be logged or persisted directly.
type Session struct {
	Token     string
	ExpiresAt time.Time
}

// LoginInput is normalized by Service before persistence queries occur.
type LoginInput struct {
	TenantSlug string
	Identifier string
	Password   string
	MFACode    string
	IP         string
	UserAgent  string
}

// Store is the persistence boundary for identity operations. It makes login
// policy testable without a PostgreSQL server and prevents HTTP code from
// accessing the connection pool directly.
type Store interface {
	ResolveTenant(ctx context.Context, slug string) (tenantID string, ok bool, err error)
	FindUser(ctx context.Context, tenantID, identifier string) (User, bool, error)
	CreateSession(ctx context.Context, user User, tokenHash []byte, expiresAt time.Time, ip, userAgent string) error
	SessionPrincipal(ctx context.Context, tenantID string, tokenHash []byte) (Principal, bool, error)
	InvalidateSession(ctx context.Context, tenantID string, tokenHash []byte) error
	UpdatePasswordHash(ctx context.Context, tenantID, userID, passwordHash string) error
}

// Service owns login timing protections, session creation and password rehash.
type Service struct {
	store      Store
	hasher     argon2id.Hasher
	dummyHash  string
	sessionTTL time.Duration
	now        func() time.Time
	mfa        MFAVerifier
}

// RequireMFA enables mandatory TOTP verification for successful privileged
// staff logins. The user record determines whether the current principal has
// the explicit auth.mfa_required permission. It is intended to be called
// during process construction, before the service is exposed through HTTP.
func (s *Service) RequireMFA(verifier MFAVerifier) error {
	if s == nil {
		return errors.New("auth: service is required")
	}
	if verifier == nil {
		return errors.New("auth: MFA verifier is required")
	}
	s.mfa = verifier
	return nil
}

// NewService creates a Service. It creates a dummy Argon2id hash once so a
// nonexistent account still pays the same verification cost as a real one.
func NewService(store Store, hasher argon2id.Hasher, sessionTTL time.Duration) (*Service, error) {
	if store == nil {
		return nil, errors.New("auth: store is required")
	}
	if sessionTTL <= 0 {
		return nil, errors.New("auth: session TTL must be positive")
	}
	dummyHash, err := hasher.Hash("netcore-invalid-login-password")
	if err != nil {
		return nil, fmt.Errorf("auth: create dummy password hash: %w", err)
	}
	return &Service{
		store:      store,
		hasher:     hasher,
		dummyHash:  dummyHash,
		sessionTTL: sessionTTL,
		now:        time.Now,
	}, nil
}

// Login returns a new session for a valid active user. It executes password
// verification against a dummy hash if tenant or user lookup fails, preventing
// the lookup result from becoming a timing oracle.
func (s *Service) Login(ctx context.Context, in LoginInput) (session Session, principal Principal, err error) {
	in.TenantSlug = strings.ToLower(strings.TrimSpace(in.TenantSlug))
	in.Identifier = NormalizeLoginIdentifier(in.Identifier)
	if in.TenantSlug == "" || in.Identifier == "" || in.Password == "" {
		return Session{}, Principal{}, ErrInvalidCredentials
	}

	tenantID, tenantFound, err := s.store.ResolveTenant(ctx, in.TenantSlug)
	if err != nil {
		return Session{}, Principal{}, fmt.Errorf("auth: resolve tenant: %w", err)
	}
	user := User{}
	userFound := false
	if tenantFound {
		user, userFound, err = s.store.FindUser(ctx, tenantID, in.Identifier)
		if err != nil {
			return Session{}, Principal{}, fmt.Errorf("auth: find user: %w", err)
		}
	}

	hash := s.dummyHash
	if userFound {
		hash = user.PasswordHash
	}
	matched, verifyErr := s.hasher.Verify(in.Password, hash)
	if verifyErr != nil || !tenantFound || !userFound || !matched || user.Status != "ACTIVE" || user.TenantID != tenantID || user.ID == "" || (!user.RequiresMFA && !user.EmailVerified) {
		return Session{}, Principal{}, ErrInvalidCredentials
	}
	if s.mfa != nil && user.RequiresMFA {
		if err := s.mfa.VerifyTOTP(ctx, user.TenantID, user.ID, strings.TrimSpace(in.MFACode)); err != nil {
			// The public login response must not disclose whether an account has
			// an enrolled device or whether a code was replayed.
			if errors.Is(err, ErrInvalidMFA) {
				return Session{}, Principal{}, ErrInvalidCredentials
			}
			return Session{}, Principal{}, fmt.Errorf("auth: verify MFA: %w", err)
		}
	}

	if needsRehash, err := s.hasher.NeedsRehash(hash); err == nil && needsRehash {
		newHash, err := s.hasher.Hash(in.Password)
		if err != nil {
			return Session{}, Principal{}, fmt.Errorf("auth: rehash password: %w", err)
		}
		if err := s.store.UpdatePasswordHash(ctx, user.TenantID, user.ID, newHash); err != nil {
			return Session{}, Principal{}, fmt.Errorf("auth: persist password rehash: %w", err)
		}
	}

	token, tokenHash, err := newToken()
	if err != nil {
		return Session{}, Principal{}, err
	}
	expiresAt := s.now().UTC().Add(s.sessionTTL)
	if err := s.store.CreateSession(ctx, user, tokenHash, expiresAt, in.IP, in.UserAgent); err != nil {
		return Session{}, Principal{}, fmt.Errorf("auth: create session: %w", err)
	}
	principal, ok, err := s.store.SessionPrincipal(ctx, user.TenantID, tokenHash)
	if err != nil {
		return Session{}, Principal{}, fmt.Errorf("auth: load principal: %w", err)
	}
	if !ok {
		return Session{}, Principal{}, errors.New("auth: created session could not be loaded")
	}
	return Session{Token: user.TenantID + "." + token, ExpiresAt: expiresAt}, principal, nil
}

// Authenticate resolves a cookie token to an active session principal.
func (s *Service) Authenticate(ctx context.Context, cookieValue string) (Principal, error) {
	tenantID, rawToken, err := splitSessionToken(cookieValue)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	principal, ok, err := s.store.SessionPrincipal(ctx, tenantID, digest(rawToken))
	if err != nil {
		return Principal{}, fmt.Errorf("auth: load session: %w", err)
	}
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	if principal.SessionCreatedAt.IsZero() || principal.SessionExpiresAt.IsZero() {
		return Principal{}, ErrUnauthenticated
	}
	policyExpiresAt := principal.SessionCreatedAt.Add(s.sessionTTL)
	if policyExpiresAt.Before(principal.SessionExpiresAt) {
		principal.SessionExpiresAt = policyExpiresAt
	}
	if !principal.SessionExpiresAt.After(s.now()) {
		return Principal{}, ErrUnauthenticated
	}
	return principal, nil
}

// Logout invalidates the current browser session. Unknown or malformed tokens
// still receive the same successful boundary response, preserving idempotency.
func (s *Service) Logout(ctx context.Context, cookieValue string) error {
	tenantID, rawToken, err := splitSessionToken(cookieValue)
	if err != nil {
		return nil
	}
	if err := s.store.InvalidateSession(ctx, tenantID, digest(rawToken)); err != nil {
		return fmt.Errorf("auth: invalidate session: %w", err)
	}
	return nil
}

func newToken() (raw string, hash []byte, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, fmt.Errorf("auth: generate session token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b[:])
	return raw, digest(raw), nil
}

func digest(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func splitSessionToken(value string) (tenantID, rawToken string, err error) {
	tenantID, rawToken, ok := strings.Cut(value, ".")
	if !ok || !validUUID(tenantID) || len(rawToken) != 43 {
		return "", "", ErrUnauthenticated
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(rawToken); err != nil {
		return "", "", ErrUnauthenticated
	}
	return tenantID, rawToken, nil
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if !((value[i] >= '0' && value[i] <= '9') || (value[i] >= 'a' && value[i] <= 'f') || (value[i] >= 'A' && value[i] <= 'F')) {
			return false
		}
	}
	return true
}
