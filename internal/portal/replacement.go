package portal

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
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
	VerifyChallenge(context.Context, string, string, string) error
}

// ReplacementService changes no entitlement in the browser request. Only the
// RADIUS function may consume a verified pending request and swap device_id.
type ReplacementService struct {
	store ReplacementStore
	auth  interface {
		VerifyCurrentPassword(context.Context, auth.Principal, string) error
	}
}

func NewReplacementService(store ReplacementStore, authService interface {
	VerifyCurrentPassword(context.Context, auth.Principal, string) error
}) (*ReplacementService, error) {
	if store == nil || authService == nil {
		return nil, errors.New("portal: replacement dependencies are required")
	}
	return &ReplacementService{store: store, auth: authService}, nil
}

func (s *ReplacementService) Begin(ctx context.Context, principal auth.Principal, password, subscriptionID, targetMAC, nas string) error {
	canonical, ok := security.NormalizeMAC(targetMAC)
	if !validUUID(principal.TenantID) || !validUUID(principal.UserID) || !validUUID(subscriptionID) || !ok {
		return ErrReplacementNotEligible
	}
	parsed, err := netip.ParseAddr(nas)
	if err != nil {
		return ErrReplacementNotEligible
	}
	if err := s.auth.VerifyCurrentPassword(ctx, principal, password); err != nil {
		return err
	}
	candidate, err := s.store.Candidate(ctx, principal.TenantID, principal.UserID, subscriptionID, canonical, parsed.String())
	if err != nil {
		return err
	}
	challengeID := uuid.NewString()
	if err = s.store.CreateChallenge(ctx, principal.TenantID, principal.UserID, candidate, challengeID, time.Now().UTC().Add(10*time.Minute)); err != nil {
		return err
	}
	return s.store.VerifyChallenge(ctx, principal.TenantID, principal.UserID, challengeID)
}
