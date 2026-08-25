package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
	"github.com/netcore-isp/netcore/pkg/crypto/totp"
)

var (
	// ErrInvalidMFA hides whether a device exists, a code expired, or a code
	// was replayed. The HTTP boundary must use the same response for each.
	ErrInvalidMFA     = errors.New("auth: invalid MFA code")
	ErrMFAUnavailable = errors.New("auth: MFA temporarily unavailable")
)

// TOTPDevice is security metadata only. SecretRef is a SecretStore path, not
// the TOTP secret and never a value that may be returned to an API client.
type TOTPDevice struct {
	ID              string
	TenantID        string
	UserID          string
	SecretRef       string
	Envelope        MFASecretEnvelope
	LastUsedCounter int64
}

// MFAStore persists device metadata and atomically consumes a TOTP counter.
// ConsumeTOTPCounter must only succeed when counter is greater than the
// previously accepted counter, preventing code replay across concurrent
// requests and across API instances.
type MFAStore interface {
	ActiveTOTPDevice(ctx context.Context, tenantID, userID string) (TOTPDevice, bool, error)
	ConsumeTOTPCounter(ctx context.Context, device TOTPDevice, counter int64) (bool, error)
}

// SecretResolver is implemented by the deployment's secret provider. It keeps
// credential-provider details outside the authentication package and ensures
// PostgreSQL never receives a TOTP secret value.
type SecretResolver interface {
	Resolve(ctx context.Context, reference string) (string, error)
}

// MFAService verifies a configured TOTP device. Enrollment and HTTP wiring
// intentionally wait for a production SecretResolver: a partial feature that
// stores MFA secrets in the application database would be less secure than no
// MFA feature at all.
type MFAService struct {
	store   MFAStore
	secrets SecretResolver
	wrapper envelope.KeyWrapper
	now     func() time.Time
	maxSkew int
}

func NewMFAService(store MFAStore, secrets SecretResolver, wrapper envelope.KeyWrapper) (*MFAService, error) {
	if store == nil {
		return nil, errors.New("auth: MFA store is required")
	}
	if secrets == nil {
		return nil, errors.New("auth: MFA secret resolver is required")
	}
	if wrapper == nil {
		return nil, errors.New("auth: MFA key wrapper is required")
	}
	return &MFAService{
		store:   store,
		secrets: secrets,
		wrapper: wrapper,
		now:     time.Now,
		maxSkew: 1,
	}, nil
}

// VerifyTOTP validates a current or adjacent TOTP period, then persists the
// matched period atomically. The returned error is deliberately non-specific
// for attacker-controlled failures.
func (s *MFAService) VerifyTOTP(ctx context.Context, tenantID, userID, code string) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(userID) == "" {
		return ErrInvalidMFA
	}
	device, found, err := s.store.ActiveTOTPDevice(ctx, tenantID, userID)
	if err != nil {
		return fmt.Errorf("%w: load device", ErrMFAUnavailable)
	}
	if !found || device.TenantID != tenantID || device.UserID != userID {
		return ErrInvalidMFA
	}
	var secret string
	if device.Envelope.complete() {
		if device.SecretRef != "" {
			return ErrInvalidMFA
		}
		secret, err = OpenTOTPSecret(ctx, s.wrapper, tenantID, userTOTPMFASubject, userID, device.Envelope)
		if err != nil {
			return ErrInvalidMFA
		}
	} else if device.Envelope.present() || device.SecretRef == "" {
		return ErrInvalidMFA
	} else {
		secret, err = s.secrets.Resolve(ctx, device.SecretRef)
		if err != nil || secret == "" {
			return ErrMFAUnavailable
		}
	}
	counter, matched, err := totp.Verify(secret, code, s.now(), totp.DefaultDigits, s.maxSkew)
	if err != nil {
		return ErrMFAUnavailable
	}
	if !matched {
		return ErrInvalidMFA
	}
	consumed, err := s.store.ConsumeTOTPCounter(ctx, device, counter)
	if err != nil {
		return ErrMFAUnavailable
	}
	if !consumed {
		return ErrInvalidMFA
	}
	return nil
}
