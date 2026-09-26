package portal

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

var ErrReplacementNotEligible = errors.New("portal: replacement not eligible")

type ReplacementCandidate struct {
	Email          string
	SubscriptionID string
	CustomerID     string
	OldDeviceID    string
	NASID          string
	TargetMAC      string
}

type ReplacementStore interface {
	Candidate(context.Context, string, string, string, string, string) (ReplacementCandidate, error)
	CreateChallenge(context.Context, string, string, ReplacementCandidate, string, time.Time) error
	ChallengeEmail(context.Context, string, string, string) (string, error)
	VerifyChallenge(context.Context, string, string, string) error
}

// ReplacementService changes no entitlement in the browser request. Only the
// RADIUS function may consume a verified pending request and swap device_id.
type ReplacementService struct {
	store ReplacementStore
	otp   *auth.OTPService
}

func NewReplacementService(store ReplacementStore, otp *auth.OTPService) (*ReplacementService, error) {
	if store == nil || otp == nil {
		return nil, errors.New("portal: replacement dependencies are required")
	}
	return &ReplacementService{store: store, otp: otp}, nil
}

func (s *ReplacementService) Begin(ctx context.Context, tenantID, userID, subscriptionID, targetMAC, nas string) (auth.IssuedOTP, error) {
	canonical, ok := security.NormalizeMAC(targetMAC)
	if !validUUID(tenantID) || !validUUID(userID) || !validUUID(subscriptionID) || !ok {
		return auth.IssuedOTP{}, ErrReplacementNotEligible
	}
	parsed, err := netip.ParseAddr(nas)
	if err != nil {
		return auth.IssuedOTP{}, ErrReplacementNotEligible
	}
	candidate, err := s.store.Candidate(ctx, tenantID, userID, subscriptionID, canonical, parsed.String())
	if err != nil {
		return auth.IssuedOTP{}, err
	}
	issued, err := s.otp.IssueForEmail(ctx, auth.OTPDeviceReplacement, candidate.Email)
	if err != nil {
		return auth.IssuedOTP{}, err
	}
	if err = s.store.CreateChallenge(ctx, tenantID, userID, candidate, issued.ChallengeID, issued.ExpiresAt); err != nil {
		return auth.IssuedOTP{}, err
	}
	return issued, nil
}

func (s *ReplacementService) Verify(ctx context.Context, tenantID, userID, challengeID, code string) error {
	if !validUUID(tenantID) || !validUUID(userID) || len(challengeID) > 128 {
		return ErrReplacementNotEligible
	}
	email, err := s.store.ChallengeEmail(ctx, tenantID, userID, challengeID)
	if err != nil {
		return err
	}
	if err = s.otp.VerifyForEmail(ctx, auth.OTPDeviceReplacement, challengeID, email, code); err != nil {
		return err
	}
	return s.store.VerifyChallenge(ctx, tenantID, userID, challengeID)
}
