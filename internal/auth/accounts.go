package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
)

var (
	ErrInvalidAccountInput = errors.New("auth: invalid account input")
	ErrAccountUnavailable  = errors.New("auth: account service unavailable")
)

// AccountStore owns the durable state changes for public customer accounts.
// It intentionally has no method for assigning a role or privilege: portal
// sign-up can create only an email-verified customer profile.
type AccountStore interface {
	ResolveTenant(ctx context.Context, slug string) (tenantID string, ok bool, err error)
	PrepareEmailRegistration(ctx context.Context, tenantID, email, passwordHash string) error
	VerifyEmailAndEnsureCustomer(ctx context.Context, tenantID, email string) error
	ResetVerifiedPassword(ctx context.Context, tenantID, email, passwordHash string) error
}

// AccountService composes Argon2id password handling with recipient-bound OTP
// challenges. It contains no SMTP/Resend credentials and does not expose
// whether an e-mail address already belongs to a customer.
type AccountService struct {
	store  AccountStore
	hasher argon2id.Hasher
	otp    *OTPService
	now    func() time.Time
}

type RegistrationInput struct {
	TenantSlug string
	Email      string
	Password   string
}

type EmailVerificationInput struct {
	TenantSlug  string
	Email       string
	ChallengeID string
	Code        string
}

type PasswordResetInput struct {
	TenantSlug  string
	Email       string
	ChallengeID string
	Code        string
	Password    string
}

func NewAccountService(store AccountStore, hasher argon2id.Hasher, otp *OTPService) (*AccountService, error) {
	if store == nil {
		return nil, errors.New("auth: account store is required")
	}
	if otp == nil {
		return nil, errors.New("auth: OTP service is required")
	}
	return &AccountService{store: store, hasher: hasher, otp: otp, now: time.Now}, nil
}

// BeginRegistration creates or refreshes only an unverified customer identity
// and delivers a verification code. A verified identity is never overwritten.
func (s *AccountService) BeginRegistration(ctx context.Context, input RegistrationInput) (IssuedOTP, error) {
	tenantSlug, email, err := normalizeAccountIdentity(input.TenantSlug, input.Email)
	if err != nil {
		return IssuedOTP{}, err
	}
	if !validCustomerPassword(input.Password) {
		return IssuedOTP{}, ErrInvalidAccountInput
	}
	tenantID, err := s.resolveTenant(ctx, tenantSlug)
	if err != nil {
		return IssuedOTP{}, err
	}
	passwordHash, err := s.hasher.Hash(input.Password)
	if err != nil {
		return IssuedOTP{}, fmt.Errorf("%w: hash password", ErrAccountUnavailable)
	}
	if err := s.store.PrepareEmailRegistration(ctx, tenantID, email, passwordHash); err != nil {
		return IssuedOTP{}, fmt.Errorf("%w: prepare registration", ErrAccountUnavailable)
	}
	return s.otp.IssueForEmail(ctx, OTPEmailVerification, email)
}

// VerifyRegistration consumes the recipient-bound code before atomically
// marking the e-mail verified and linking a customer profile.
func (s *AccountService) VerifyRegistration(ctx context.Context, input EmailVerificationInput) error {
	tenantSlug, email, err := normalizeAccountIdentity(input.TenantSlug, input.Email)
	if err != nil {
		return err
	}
	tenantID, err := s.resolveTenant(ctx, tenantSlug)
	if err != nil {
		return err
	}
	if err := s.otp.VerifyForEmail(ctx, OTPEmailVerification, input.ChallengeID, email, input.Code); err != nil {
		return err
	}
	if err := s.store.VerifyEmailAndEnsureCustomer(ctx, tenantID, email); err != nil {
		return fmt.Errorf("%w: verify registration", ErrAccountUnavailable)
	}
	return nil
}

// RequestPasswordReset always sends a recipient-bound code for a syntactically
// valid address in the configured tenant. The confirmation store update is a
// no-op for an unknown or unverified address, which keeps this workflow from
// becoming an account-enumeration oracle.
func (s *AccountService) RequestPasswordReset(ctx context.Context, tenantSlug, email string) (IssuedOTP, error) {
	tenantSlug, email, err := normalizeAccountIdentity(tenantSlug, email)
	if err != nil {
		return IssuedOTP{}, err
	}
	if _, err := s.resolveTenant(ctx, tenantSlug); err != nil {
		return IssuedOTP{}, err
	}
	return s.otp.IssueForEmail(ctx, OTPPasswordReset, email)
}

// ConfirmPasswordReset changes a password only when the action- and
// recipient-bound code is valid. The store deliberately reports no affected
// row count so caller-visible behavior remains generic for unknown accounts.
func (s *AccountService) ConfirmPasswordReset(ctx context.Context, input PasswordResetInput) error {
	tenantSlug, email, err := normalizeAccountIdentity(input.TenantSlug, input.Email)
	if err != nil {
		return err
	}
	if !validCustomerPassword(input.Password) {
		return ErrInvalidAccountInput
	}
	tenantID, err := s.resolveTenant(ctx, tenantSlug)
	if err != nil {
		return err
	}
	if err := s.otp.VerifyForEmail(ctx, OTPPasswordReset, input.ChallengeID, email, input.Code); err != nil {
		return err
	}
	passwordHash, err := s.hasher.Hash(input.Password)
	if err != nil {
		return fmt.Errorf("%w: hash password", ErrAccountUnavailable)
	}
	if err := s.store.ResetVerifiedPassword(ctx, tenantID, email, passwordHash); err != nil {
		return fmt.Errorf("%w: reset password", ErrAccountUnavailable)
	}
	return nil
}

func (s *AccountService) resolveTenant(ctx context.Context, slug string) (string, error) {
	tenantID, found, err := s.store.ResolveTenant(ctx, slug)
	if err != nil {
		return "", fmt.Errorf("%w: resolve tenant", ErrAccountUnavailable)
	}
	if !found || tenantID == "" {
		return "", ErrInvalidAccountInput
	}
	return tenantID, nil
}

func normalizeAccountIdentity(tenantSlug, email string) (string, string, error) {
	tenantSlug = strings.ToLower(strings.TrimSpace(tenantSlug))
	if tenantSlug == "" {
		return "", "", ErrInvalidAccountInput
	}
	email, _, ok := emailOTPBinding(email)
	if !ok {
		return "", "", ErrInvalidAccountInput
	}
	return tenantSlug, email, nil
}

func validCustomerPassword(value string) bool {
	return len(value) >= 12 && len(value) <= 1024
}
