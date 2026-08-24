package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryOTPStore struct {
	digests  map[string][]byte
	purposes map[string]string
	bindings map[string][]byte
	attempts map[string]int64
	deleted  []string
	err      error
}

func newMemoryOTPStore() *memoryOTPStore {
	return &memoryOTPStore{
		digests:  make(map[string][]byte),
		purposes: make(map[string]string),
		bindings: make(map[string][]byte),
		attempts: make(map[string]int64),
	}
}

func (s *memoryOTPStore) CreateOTPChallenge(_ context.Context, id, purpose string, binding, digest []byte, _ time.Duration) error {
	if s.err != nil {
		return s.err
	}
	if _, exists := s.digests[id]; exists {
		return errors.New("collision")
	}
	s.digests[id] = append([]byte(nil), digest...)
	s.purposes[id] = purpose
	s.bindings[id] = append([]byte(nil), binding...)
	return nil
}

func (s *memoryOTPStore) ConsumeOTPChallenge(_ context.Context, id, purpose string, binding, digest []byte, maxAttempts int64) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	s.attempts[id]++
	matched := s.purposes[id] == purpose && string(s.bindings[id]) == string(binding) && string(s.digests[id]) == string(digest)
	if matched || s.attempts[id] >= maxAttempts {
		delete(s.digests, id)
		delete(s.purposes, id)
		delete(s.bindings, id)
	}
	return matched, nil
}

func (s *memoryOTPStore) DeleteOTPChallenge(_ context.Context, id string) error {
	delete(s.digests, id)
	delete(s.purposes, id)
	delete(s.bindings, id)
	s.deleted = append(s.deleted, id)
	return nil
}

type recordingNotifier struct {
	code string
	err  error
}

func (n *recordingNotifier) SendOTP(_ context.Context, _ OTPPurpose, _ string, code string, _ time.Time) error {
	n.code = code
	return n.err
}

func TestOTPServiceIssuesAndConsumesOneChallenge(t *testing.T) {
	store := newMemoryOTPStore()
	notifier := &recordingNotifier{}
	service, err := NewOTPService(store, notifier)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC) }
	issued, err := service.Issue(context.Background(), OTPPasswordReset, "+2348012345678")
	if err != nil {
		t.Fatal(err)
	}
	if issued.ChallengeID == "" || notifier.code == "" || issued.ExpiresAt != service.now().Add(defaultOTPTTL) {
		t.Fatalf("unexpected issuance result: %+v", issued)
	}
	if err := service.Verify(context.Background(), OTPPasswordReset, issued.ChallengeID, notifier.code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := service.Verify(context.Background(), OTPPasswordReset, issued.ChallengeID, notifier.code); !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("reused code error = %v, want ErrInvalidOTP", err)
	}
}

func TestOTPServiceDestroysChallengeAfterFiveFailedAttempts(t *testing.T) {
	store := newMemoryOTPStore()
	notifier := &recordingNotifier{}
	service, err := NewOTPService(store, notifier)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Issue(context.Background(), OTPPhoneVerification, "+2348012345678")
	if err != nil {
		t.Fatal(err)
	}
	for range defaultOTPMaxAttempts {
		if err := service.Verify(context.Background(), OTPPhoneVerification, issued.ChallengeID, "000000"); !errors.Is(err, ErrInvalidOTP) {
			t.Fatalf("failed OTP error = %v", err)
		}
	}
	if _, exists := store.digests[issued.ChallengeID]; exists {
		t.Fatal("challenge remains after its final failed attempt")
	}
}

func TestOTPServiceBindsCodeToItsPurpose(t *testing.T) {
	store := newMemoryOTPStore()
	notifier := &recordingNotifier{}
	service, err := NewOTPService(store, notifier)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Issue(context.Background(), OTPPasswordReset, "+2348012345678")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Verify(context.Background(), OTPPhoneVerification, issued.ChallengeID, notifier.code); !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("wrong-purpose OTP error = %v, want ErrInvalidOTP", err)
	}
	if err := service.Verify(context.Background(), OTPPasswordReset, issued.ChallengeID, notifier.code); err != nil {
		t.Fatalf("correct-purpose OTP error = %v", err)
	}
}

func TestOTPServiceBindsEmailCodeToItsRecipient(t *testing.T) {
	store := newMemoryOTPStore()
	notifier := &recordingNotifier{}
	service, err := NewOTPService(store, notifier)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueForEmail(context.Background(), OTPEmailVerification, "customer@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyForEmail(context.Background(), OTPEmailVerification, issued.ChallengeID, "other@example.com", notifier.code); !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("wrong-recipient OTP error = %v, want ErrInvalidOTP", err)
	}
	if err := service.VerifyForEmail(context.Background(), OTPEmailVerification, issued.ChallengeID, "customer@example.com", notifier.code); err != nil {
		t.Fatalf("correct-recipient OTP error = %v", err)
	}
}

func TestOTPServiceRemovesUndeliveredChallenge(t *testing.T) {
	store := newMemoryOTPStore()
	notifier := &recordingNotifier{err: errors.New("provider down")}
	service, err := NewOTPService(store, notifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Issue(context.Background(), OTPPasswordReset, "+2348012345678"); !errors.Is(err, ErrOTPUnavailable) {
		t.Fatalf("Issue error = %v, want ErrOTPUnavailable", err)
	}
	if len(store.digests) != 0 || len(store.deleted) != 1 {
		t.Fatalf("undelivered challenge was retained: digests=%d deletes=%d", len(store.digests), len(store.deleted))
	}
}

func TestOTPServiceFailsClosedForStoreFailure(t *testing.T) {
	store := newMemoryOTPStore()
	store.err = errors.New("redis down")
	service, err := NewOTPService(store, &recordingNotifier{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Issue(context.Background(), OTPPasswordReset, "+2348012345678"); !errors.Is(err, ErrOTPUnavailable) {
		t.Fatalf("Issue error = %v, want ErrOTPUnavailable", err)
	}
}
