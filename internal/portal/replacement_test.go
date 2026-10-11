package portal

import (
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
func (s *replacementTestStore) VerifyChallenge(_ context.Context, _, _, id string) error {
	if id != s.challengeID {
		return ErrReplacementNotEligible
	}
	s.verified = true
	return nil
}

type replacementPasswordVerifier struct{}

func (replacementPasswordVerifier) VerifyCurrentPassword(_ context.Context, _ auth.Principal, password string) error {
	if password != "current password" {
		return auth.ErrInvalidCredentials
	}
	return nil
}

func TestReplacementRequiresCurrentPasswordBeforePendingRequest(t *testing.T) {
	store := &replacementTestStore{}
	service, err := NewReplacementService(store, replacementPasswordVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{TenantID: portalTestTenantID, UserID: portalTestUserID, Email: "customer@example.com"}
	const subscriptionID = "33333333-3333-4333-8333-333333333333"
	if err := service.Begin(context.Background(), principal, "wrong", subscriptionID, "AA:BB:CC:DD:EE:FF", "10.10.0.1"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("wrong password accepted: %v", err)
	}
	if store.challengeID != "" || store.verified {
		t.Fatal("wrong password created a replacement")
	}
	if err := service.Begin(context.Background(), principal, "current password", subscriptionID, "AA:BB:CC:DD:EE:FF", "10.10.0.1"); err != nil {
		t.Fatal(err)
	}
	if !store.verified || store.candidate.TargetMAC != "aabbccddeeff" {
		t.Fatal("password-confirmed move did not create pending request")
	}
}

func TestReplacementRejectsInvalidConnectionContext(t *testing.T) {
	store := &replacementTestStore{}
	service, err := NewReplacementService(store, replacementPasswordVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{TenantID: portalTestTenantID, UserID: portalTestUserID, Email: "customer@example.com"}
	err = service.Begin(context.Background(), principal, "current password", "33333333-3333-4333-8333-333333333333", "not-a-mac", "10.10.0.1")
	if !errors.Is(err, ErrReplacementNotEligible) || store.challengeID != "" {
		t.Fatalf("invalid context passed: %v", err)
	}
}
