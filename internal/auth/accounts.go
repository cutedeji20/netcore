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
	// Phone verification cannot be enabled until an SMS provider is wired.
	// The value remains a separate error so HTTP can report a safe, actionable
	// availability response without pretending that a code was sent.
	ErrPhoneVerificationUnavailable = errors.New("auth: phone verification is unavailable")
)

// AccountStore owns the durable state changes for public customer accounts.
// It intentionally has no method for assigning a role or privilege: portal
// sign-up can create only an email-verified customer profile.
type AccountStore interface {
	ResolveTenant(ctx context.Context, slug string) (tenantID string, ok bool, err error)
	RegistrationPolicy(ctx context.Context, tenantID string) (RegistrationPolicy, error)
	PrepareEmailRegistration(ctx context.Context, tenantID, email, phone, passwordHash string) error
	VerifyEmailAndEnsureCustomer(ctx context.Context, tenantID, email string) error
	ResetVerifiedPassword(ctx context.Context, tenantID, email, passwordHash string) error
}

// RegistrationPolicy is tenant-owned. Both switches default to false in the
// database so a new deployment does not accidentally block customer sign-up.
type RegistrationPolicy struct {
	RequireEmailVerification bool
	RequirePhoneVerification bool
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
	Phone      string
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
	if !validCustomerPassword(input.Password) || !validPhone(input.Phone) {
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
	policy, err := s.store.RegistrationPolicy(ctx, tenantID)
	if err != nil {
		return IssuedOTP{}, fmt.Errorf("%w: read registration policy", ErrAccountUnavailable)
	}
	if policy.RequirePhoneVerification {
		return IssuedOTP{}, ErrPhoneVerificationUnavailable
	}
	if err := s.store.PrepareEmailRegistration(ctx, tenantID, email, strings.TrimSpace(input.Phone), passwordHash); err != nil {
		return IssuedOTP{}, fmt.Errorf("%w: prepare registration", ErrAccountUnavailable)
	}
	if !policy.RequireEmailVerification {
		// The tenant has deliberately chosen not to require an email OTP. The
		// same atomic activation path still creates/links the customer profile;
		// email_verified_at is the existing login-eligibility marker.
		if err := s.store.VerifyEmailAndEnsureCustomer(ctx, tenantID, email); err != nil {
			return IssuedOTP{}, fmt.Errorf("%w: activate registration", ErrAccountUnavailable)
		}
		return IssuedOTP{VerificationRequired: false}, nil
	}
	issued, err := s.otp.IssueForEmail(ctx, OTPEmailVerification, email)
	if err != nil {
		return IssuedOTP{}, err
	}
	issued.VerificationRequired = true
	return issued, nil
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
	if len(value) < 4 || len(value) > 12 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func validPhone(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 16 || !strings.HasPrefix(value, "+") {
		return false
	}
	for _, c := range value[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
