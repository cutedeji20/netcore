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
)

const (
	defaultOTPTTL         = 10 * time.Minute
	defaultOTPMaxAttempts = int64(5)
	OTPCodeLength         = 6
)

var (
	// ErrInvalidOTP deliberately covers malformed, expired, consumed and
	// incorrect challenges. An endpoint must expose one response for all of
	// these cases.
	ErrInvalidOTP     = errors.New("auth: invalid OTP")
	ErrOTPUnavailable = errors.New("auth: OTP temporarily unavailable")
)

// OTPPurpose prevents one OTP type from being accepted for a different
// security action. New purposes require an explicit code review decision.
type OTPPurpose string

const (
	OTPPasswordReset     OTPPurpose = "PASSWORD_RESET"
	OTPPhoneVerification OTPPurpose = "PHONE_VERIFICATION"
	OTPPasswordlessLogin OTPPurpose = "PASSWORDLESS_LOGIN"
)

// OTPChallengeStore provides atomic, ephemeral OTP state. Its implementation
// must count failed attempts and destroy a challenge on success or exhaustion.
// It stores only an opaque ID and a fixed-length digest.
type OTPChallengeStore interface {
	CreateOTPChallenge(ctx context.Context, challengeID, purpose string, digest []byte, ttl time.Duration) error
	ConsumeOTPChallenge(ctx context.Context, challengeID, purpose string, digest []byte, maxAttempts int64) (bool, error)
	DeleteOTPChallenge(ctx context.Context, challengeID string) error
}

// OTPNotifier delivers a raw OTP to the user. Implementations may use SMS or
// e-mail, but they must never log the OTP or destination. The authentication
// package deliberately contains no provider credential or delivery SDK.
type OTPNotifier interface {
	SendOTP(ctx context.Context, purpose OTPPurpose, destination, code string, expiresAt time.Time) error
}

// OTPService issues and verifies numeric one-time passwords. It is not wired
// to HTTP until a production notifier and the relevant account workflows are
// configured; this avoids accidentally returning codes in API responses.
type OTPService struct {
	store       OTPChallengeStore
	notifier    OTPNotifier
	ttl         time.Duration
	maxAttempts int64
	now         func() time.Time
}

// IssuedOTP identifies a newly issued challenge. Code is intentionally absent:
// it belongs only in the notifier call.
type IssuedOTP struct {
	ChallengeID string
	ExpiresAt   time.Time
}

func NewOTPService(store OTPChallengeStore, notifier OTPNotifier) (*OTPService, error) {
	if store == nil {
		return nil, errors.New("auth: OTP challenge store is required")
	}
	if notifier == nil {
		return nil, errors.New("auth: OTP notifier is required")
	}
	return &OTPService{
		store:       store,
		notifier:    notifier,
		ttl:         defaultOTPTTL,
		maxAttempts: defaultOTPMaxAttempts,
		now:         time.Now,
	}, nil
}

// Issue persists a challenge before sending its code. If delivery fails, the
// challenge is removed so an undelivered code cannot later be guessed.
func (s *OTPService) Issue(ctx context.Context, purpose OTPPurpose, destination string) (IssuedOTP, error) {
	if !validOTPPurpose(purpose) || strings.TrimSpace(destination) == "" {
		return IssuedOTP{}, ErrOTPUnavailable
	}
	challengeID, err := randomChallengeID()
	if err != nil {
		return IssuedOTP{}, err
	}
	code, err := randomOTPCode()
	if err != nil {
		return IssuedOTP{}, err
	}
	expiresAt := s.now().UTC().Add(s.ttl)
	if err := s.store.CreateOTPChallenge(ctx, challengeID, string(purpose), otpDigest(challengeID, code), s.ttl); err != nil {
		return IssuedOTP{}, fmt.Errorf("%w: %v", ErrOTPUnavailable, err)
	}
	if err := s.notifier.SendOTP(ctx, purpose, strings.TrimSpace(destination), code, expiresAt); err != nil {
		_ = s.store.DeleteOTPChallenge(ctx, challengeID)
		return IssuedOTP{}, fmt.Errorf("%w: delivery failed", ErrOTPUnavailable)
	}
	return IssuedOTP{ChallengeID: challengeID, ExpiresAt: expiresAt}, nil
}

// Verify consumes a valid challenge. It treats every challenge-state failure
// identically; callers must rate-limit this operation before invoking it.
func (s *OTPService) Verify(ctx context.Context, purpose OTPPurpose, challengeID, code string) error {
	if !validOTPPurpose(purpose) || !validChallengeID(challengeID) || !validOTPCode(code) {
		return ErrInvalidOTP
	}
	matched, err := s.store.ConsumeOTPChallenge(ctx, challengeID, string(purpose), otpDigest(challengeID, code), s.maxAttempts)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOTPUnavailable, err)
	}
	if !matched {
		return ErrInvalidOTP
	}
	return nil
}

func validOTPPurpose(purpose OTPPurpose) bool {
	switch purpose {
	case OTPPasswordReset, OTPPhoneVerification, OTPPasswordlessLogin:
		return true
	default:
		return false
	}
}

func randomChallengeID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth: generate OTP challenge ID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func randomOTPCode() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth: generate OTP: %w", err)
	}
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return fmt.Sprintf("%06d", n%1_000_000), nil
}

func validChallengeID(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validOTPCode(value string) bool {
	if len(value) != OTPCodeLength {
		return false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func otpDigest(challengeID, code string) []byte {
	sum := sha256.Sum256([]byte(challengeID + "\x00" + code))
	return sum[:]
}
