package portal

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

type replacementTestStore struct {
	candidate   ReplacementCandidate
	challengeID string
	verified    bool
}

func (s *replacementTestStore) Candidate(_ context.Context, _, _, _, mac, _ string) (ReplacementCandidate, error) {
	s.candidate.TargetMAC = mac
	return s.candidate, nil
}
func (s *replacementTestStore) CreateChallenge(_ context.Context, _, _ string, _ ReplacementCandidate, id string, _ time.Time) error {
	s.challengeID = id
	return nil
}
func (s *replacementTestStore) ChallengeEmail(_ context.Context, _, _, id string) (string, error) {
	if id != s.challengeID || s.verified {
		return "", ErrReplacementNotEligible
	}
	return s.candidate.Email, nil
}
func (s *replacementTestStore) VerifyChallenge(_ context.Context, _, _, id string) error {
	if id != s.challengeID {
		return ErrReplacementNotEligible
	}
	s.verified = true
	return nil
}

type replacementOTPStore struct {
	id, purpose     string
	binding, digest []byte
	used            bool
}

func (s *replacementOTPStore) CreateOTPChallenge(_ context.Context, id, purpose string, binding, digest []byte, _ time.Duration) error {
	s.id = id
	s.purpose = purpose
	s.binding = append([]byte(nil), binding...)
	s.digest = append([]byte(nil), digest...)
	return nil
}
func (s *replacementOTPStore) ConsumeOTPChallenge(_ context.Context, id, purpose string, binding, digest []byte, _ int64) (bool, error) {
	if s.used || id != s.id || purpose != s.purpose || !bytes.Equal(binding, s.binding) || !bytes.Equal(digest, s.digest) {
		return false, nil
	}
	s.used = true
	return true, nil
}
func (s *replacementOTPStore) DeleteOTPChallenge(context.Context, string) error { return nil }

type replacementNotifier struct{ code string }

func (n *replacementNotifier) SendOTP(_ context.Context, _ auth.OTPPurpose, _, code string, _ time.Time) error {
	n.code = code
	return nil
}

func TestReplacementNeedsEmailCodeBeforePendingRequest(t *testing.T) {
	store := &replacementTestStore{candidate: ReplacementCandidate{Email: "customer@example.com"}}
	notifier := &replacementNotifier{}
	otp, err := auth.NewOTPService(&replacementOTPStore{}, notifier)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewReplacementService(store, otp)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Begin(context.Background(), portalTestTenantID, portalTestUserID, "33333333-3333-4333-8333-333333333333", "AA:BB:CC:DD:EE:FF", "10.10.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if store.verified || store.candidate.TargetMAC != "aabbccddeeff" {
		t.Fatal("request altered entitlement or failed to canonicalize MAC")
	}
	if err = service.Verify(context.Background(), portalTestTenantID, portalTestUserID, issued.ChallengeID, "000000"); !errors.Is(err, auth.ErrInvalidOTP) {
		t.Fatalf("incorrect code accepted: %v", err)
	}
	if store.verified {
		t.Fatal("wrong code created pending request")
	}
	if err = service.Verify(context.Background(), portalTestTenantID, portalTestUserID, issued.ChallengeID, notifier.code); err != nil {
		t.Fatal(err)
	}
	if !store.verified {
		t.Fatal("verified code did not create pending request")
	}
	if err = service.Verify(context.Background(), portalTestTenantID, portalTestUserID, issued.ChallengeID, notifier.code); err == nil {
		t.Fatal("replayed code accepted")
	}
}

func TestReplacementRejectsInvalidConnectionContext(t *testing.T) {
	store := &replacementTestStore{}
	otp, err := auth.NewOTPService(&replacementOTPStore{}, &replacementNotifier{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewReplacementService(store, otp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Begin(context.Background(), portalTestTenantID, portalTestUserID, "33333333-3333-4333-8333-333333333333", "not-a-mac", "10.10.0.1")
	if !errors.Is(err, ErrReplacementNotEligible) || store.challengeID != "" {
		t.Fatalf("invalid context passed: %v", err)
	}
}
