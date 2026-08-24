package integrations

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

var (
	ErrStepUpFailed     = errors.New("integrations: step-up verification failed")
	ErrInvalidSettings  = errors.New("integrations: invalid provider settings")
	ErrStoreUnavailable = errors.New("integrations: configuration store unavailable")
)

const (
	StatusDisconnected = "DISCONNECTED"
	StatusDisabled     = "DISABLED"
	StatusActive       = "ACTIVE"
)

// Record is the persistence representation of a configured provider. The
// Envelope is database-safe ciphertext; provider plaintext is never a field.
type Record struct {
	TenantID          string
	Provider          Provider
	Status            string
	Envelope          CredentialEnvelope
	SenderEmail       string
	PaystackMode      string
	LastTestedAt      time.Time
	LastTestSucceeded bool
	ActivatedAt       time.Time
	UpdatedAt         time.Time
	UpdatedBy         string
}

// Store owns tenant-scoped persistence. Its PostgreSQL implementation is added
// separately so service policy remains testable without a database.
type Store interface {
	Save(context.Context, Record) error
	List(context.Context, string) ([]Record, error)
	Disable(context.Context, string, Provider, string, time.Time) error
	Disconnect(context.Context, string, Provider, string, time.Time) error
}

// Snapshot is safe to return to a privileged browser. It deliberately omits
// every envelope field and every Key Vault identifier.
type Snapshot struct {
	Provider          Provider   `json:"provider"`
	Status            string     `json:"status"`
	SenderEmail       string     `json:"sender_email,omitempty"`
	PaystackMode      string     `json:"paystack_mode,omitempty"`
	LastTestedAt      *time.Time `json:"last_tested_at,omitempty"`
	LastTestSucceeded *bool      `json:"last_test_succeeded,omitempty"`
	ActivatedAt       time.Time  `json:"activated_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// StepUpVerifier is fulfilled by auth.Service without making the integrations
// package depend on session storage or TOTP-secret storage details.
type StepUpVerifier interface {
	VerifyStepUp(context.Context, auth.StepUpInput) error
}

// ProviderValidator proves a submitted credential is usable before the
// envelope becomes active. Implementations must not log or retain it.
type ProviderValidator interface {
	Validate(context.Context, ConfigureInput) error
}

type acceptingProviderValidator struct{}

func (acceptingProviderValidator) Validate(context.Context, ConfigureInput) error { return nil }

type ConfigureInput struct {
	Principal    auth.Principal
	Password     string
	MFACode      string
	Provider     Provider
	Credential   []byte
	SenderEmail  string
	PaystackMode string
}

type Service struct {
	store     Store
	wrapper   KeyWrapper
	stepUp    StepUpVerifier
	validator ProviderValidator
	now       func() time.Time
}

func NewService(store Store, wrapper KeyWrapper, stepUp StepUpVerifier, validators ...ProviderValidator) (*Service, error) {
	if store == nil {
		return nil, errors.New("integrations: store is required")
	}
	if wrapper == nil {
		return nil, errors.New("integrations: key wrapper is required")
	}
	if stepUp == nil {
		return nil, errors.New("integrations: step-up verifier is required")
	}
	if len(validators) > 1 || (len(validators) == 1 && validators[0] == nil) {
		return nil, errors.New("integrations: provider validator is invalid")
	}
	validator := ProviderValidator(acceptingProviderValidator{})
	if len(validators) == 1 {
		validator = validators[0]
	}
	return &Service{store: store, wrapper: wrapper, stepUp: stepUp, validator: validator, now: time.Now}, nil
}

// Configure validates provider metadata, verifies fresh staff credentials, and
// persists only a newly encrypted credential envelope.
func (s *Service) Configure(ctx context.Context, input ConfigureInput) error {
	if s == nil || s.store == nil || s.wrapper == nil || s.stepUp == nil || s.validator == nil {
		return ErrStoreUnavailable
	}
	if !input.Provider.Valid() || !validTenantID(input.Principal.TenantID) || strings.TrimSpace(input.Principal.UserID) == "" || !validProviderSettings(input) {
		return ErrInvalidSettings
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: input.Principal, Password: input.Password, MFACode: input.MFACode}); err != nil {
		return ErrStepUpFailed
	}
	if err := s.validator.Validate(ctx, input); err != nil {
		return ErrCredentialInvalid
	}
	envelope, err := EncryptCredential(ctx, s.wrapper, input.Principal.TenantID, input.Provider, input.Credential)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	record := Record{
		TenantID: input.Principal.TenantID, Provider: input.Provider, Status: StatusActive,
		Envelope: envelope, SenderEmail: strings.TrimSpace(input.SenderEmail),
		PaystackMode: strings.ToUpper(strings.TrimSpace(input.PaystackMode)),
		LastTestedAt: now, LastTestSucceeded: true, ActivatedAt: now, UpdatedAt: now, UpdatedBy: input.Principal.UserID,
	}
	if err := s.store.Save(ctx, record); err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

func (s *Service) List(ctx context.Context, tenantID string) ([]Snapshot, error) {
	if s == nil || s.store == nil || !validTenantID(tenantID) {
		return nil, ErrStoreUnavailable
	}
	records, err := s.store.List(ctx, tenantID)
	if err != nil {
		return nil, ErrStoreUnavailable
	}
	out := make([]Snapshot, 0, len(records))
	for _, record := range records {
		if record.TenantID != tenantID || !record.Provider.Valid() {
			return nil, ErrStoreUnavailable
		}
		snapshot := Snapshot{Provider: record.Provider, Status: record.Status, SenderEmail: record.SenderEmail, PaystackMode: record.PaystackMode, ActivatedAt: record.ActivatedAt, UpdatedAt: record.UpdatedAt}
		if !record.LastTestedAt.IsZero() {
			result := record.LastTestSucceeded
			testedAt := record.LastTestedAt
			snapshot.LastTestedAt = &testedAt
			snapshot.LastTestSucceeded = &result
		}
		out = append(out, snapshot)
	}
	return out, nil
}

func (s *Service) Disable(ctx context.Context, principal auth.Principal, password, mfaCode string, provider Provider) error {
	return s.changeStatus(ctx, principal, password, mfaCode, provider, false)
}

func (s *Service) Disconnect(ctx context.Context, principal auth.Principal, password, mfaCode string, provider Provider) error {
	return s.changeStatus(ctx, principal, password, mfaCode, provider, true)
}

func (s *Service) changeStatus(ctx context.Context, principal auth.Principal, password, mfaCode string, provider Provider, disconnect bool) error {
	if s == nil || s.store == nil || s.stepUp == nil || !provider.Valid() || !validTenantID(principal.TenantID) || strings.TrimSpace(principal.UserID) == "" {
		return ErrInvalidSettings
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return ErrStepUpFailed
	}
	at := s.now().UTC()
	var err error
	if disconnect {
		err = s.store.Disconnect(ctx, principal.TenantID, provider, principal.UserID, at)
	} else {
		err = s.store.Disable(ctx, principal.TenantID, provider, principal.UserID, at)
	}
	if err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

func validProviderSettings(input ConfigureInput) bool {
	switch input.Provider {
	case ProviderResend:
		parsed, err := mail.ParseAddress(strings.TrimSpace(input.SenderEmail))
		return err == nil && parsed.Address != "" && strings.TrimSpace(input.PaystackMode) == ""
	case ProviderPaystack:
		return strings.TrimSpace(input.SenderEmail) == "" && (strings.EqualFold(strings.TrimSpace(input.PaystackMode), "TEST") || strings.EqualFold(strings.TrimSpace(input.PaystackMode), "LIVE"))
	default:
		return false
	}
}
