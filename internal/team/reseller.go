package team

import (
	"context"
	"errors"

	"github.com/netcore-isp/netcore/internal/auth"
)

var ErrResellerInvalid = errors.New("team: reseller designation invalid")

type MutationActor struct{ UserID string }

type ResellerStore interface {
	SetReseller(context.Context, string, string, bool, MutationActor) error
	IsReseller(context.Context, string, string) (bool, error)
}

type ResellerService struct {
	store  ResellerStore
	stepUp StepUpVerifier
}

func NewResellerService(store ResellerStore, stepUp StepUpVerifier) (*ResellerService, error) {
	if store == nil || stepUp == nil {
		return nil, ErrResellerInvalid
	}
	return &ResellerService{store: store, stepUp: stepUp}, nil
}

func (s *ResellerService) SetReseller(ctx context.Context, principal auth.Principal, userID string, enabled bool, password, mfaCode string) error {
	if s == nil || !validUUID(principal.TenantID) || !validUUID(principal.UserID) || !validUUID(userID) || !principal.HasPermission("team.write") {
		return ErrResellerInvalid
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return ErrStepUpFailed
	}
	return s.store.SetReseller(ctx, principal.TenantID, userID, enabled, MutationActor{UserID: principal.UserID})
}
