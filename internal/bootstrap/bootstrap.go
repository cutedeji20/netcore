// Package bootstrap provides the one-time, local first-operator ceremony.
// It is deliberately not an HTTP feature: a public endpoint for creating the
// first privileged account would be a permanent and high-impact attack path.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/team"
	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
	"github.com/netcore-isp/netcore/pkg/crypto/totp"
)

var (
	ErrAlreadyBootstrapped = errors.New("bootstrap: an operator or tenant already exists")
	ErrInvalidInput        = errors.New("bootstrap: invalid first administrator input")
)

// SecretResolver resolves the pre-provisioned MFA secret without exposing a
// secret-store implementation to the bootstrap domain.
type SecretResolver interface {
	Resolve(context.Context, string) (string, error)
}

// Record is the validated, non-secret part of the first administrator record.
// PasswordHash is already Argon2id-hashed; the raw password never reaches the
// database store or an audit record.
type Record struct {
	TenantName             string
	TenantSlug             string
	Timezone               string
	Currency               string
	Email                  string
	PasswordHash           string
	TOTPSecretRef          string
	InitialTOTPCounter     int64
	InitialRoles           []team.BuiltInRole
	InitialRoleAssignments []team.BuiltInRole
}

type Result struct {
	TenantID string
	UserID   string
}

// Store performs the atomic database ceremony. The PostgreSQL implementation
// takes an advisory transaction lock, so two console invocations cannot create
// competing first operators.
type Store interface {
	BootstrapFirstAdmin(context.Context, Record) (Result, error)
}

type Input struct {
	TenantName    string
	TenantSlug    string
	Timezone      string
	Currency      string
	Email         string
	Password      string
	TOTPSecretRef string
	TOTPCode      string
}

type Service struct {
	store   Store
	hasher  argon2id.Hasher
	secrets SecretResolver
	now     func() time.Time
}

func NewService(store Store, hasher argon2id.Hasher, secrets SecretResolver) (*Service, error) {
	if store == nil {
		return nil, errors.New("bootstrap: store is required")
	}
	if secrets == nil {
		return nil, errors.New("bootstrap: secret resolver is required")
	}
	return &Service{store: store, hasher: hasher, secrets: secrets, now: time.Now}, nil
}

// Run verifies that the on-console operator controls the pre-provisioned TOTP
// device before the one-time database transaction is attempted. The proof code
// is recorded as consumed, so the same six-digit code cannot immediately form
// the first browser session.
func (s *Service) Run(ctx context.Context, in Input) (Result, error) {
	if s == nil || s.store == nil || s.secrets == nil {
		return Result{}, errors.New("bootstrap: service is not configured")
	}
	record, err := normalize(in)
	if err != nil {
		return Result{}, err
	}
	secret, err := s.secrets.Resolve(ctx, record.TOTPSecretRef)
	if err != nil {
		return Result{}, fmt.Errorf("bootstrap: resolve TOTP secret: %w", err)
	}
	counter, matched, err := totp.Verify(secret, in.TOTPCode, s.now(), totp.DefaultDigits, 1)
	if err != nil {
		return Result{}, fmt.Errorf("bootstrap: verify TOTP secret: %w", err)
	}
	if !matched {
		return Result{}, fmt.Errorf("%w: authenticator code was not accepted", ErrInvalidInput)
	}
	record.InitialTOTPCounter = counter
	record.InitialRoles = team.BuiltInRoles()
	record.InitialRoleAssignments = []team.BuiltInRole{team.RoleAdministrator}
	record.PasswordHash, err = s.hasher.Hash(in.Password)
	if err != nil {
		return Result{}, fmt.Errorf("bootstrap: hash first administrator password: %w", err)
	}
	return s.store.BootstrapFirstAdmin(ctx, record)
}

func normalize(in Input) (Record, error) {
	result := Record{
		TenantName:    strings.TrimSpace(in.TenantName),
		TenantSlug:    strings.ToLower(strings.TrimSpace(in.TenantSlug)),
		Timezone:      strings.TrimSpace(in.Timezone),
		Currency:      strings.ToUpper(strings.TrimSpace(in.Currency)),
		Email:         strings.ToLower(strings.TrimSpace(in.Email)),
		TOTPSecretRef: strings.TrimSpace(in.TOTPSecretRef),
	}
	if len(result.TenantName) < 2 || len(result.TenantName) > 120 || !validSlug(result.TenantSlug) || !validCurrency(result.Currency) || !validSecretRef(result.TOTPSecretRef) {
		return Record{}, ErrInvalidInput
	}
	if _, err := time.LoadLocation(result.Timezone); err != nil {
		return Record{}, fmt.Errorf("%w: timezone", ErrInvalidInput)
	}
	parsed, err := mail.ParseAddress(result.Email)
	if err != nil || parsed.Address != result.Email || len(result.Email) > 254 {
		return Record{}, fmt.Errorf("%w: email", ErrInvalidInput)
	}
	if len(in.Password) < 16 || len(in.Password) > 1024 {
		return Record{}, fmt.Errorf("%w: password must be 16-1024 bytes", ErrInvalidInput)
	}
	if len(strings.TrimSpace(in.TOTPCode)) != totp.DefaultDigits {
		return Record{}, fmt.Errorf("%w: authenticator code", ErrInvalidInput)
	}
	return result, nil
}

func validSlug(value string) bool {
	if len(value) < 3 || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func validSecretRef(value string) bool {
	if len(value) < 3 || len(value) > 160 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
