package billing

import (
	"context"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/pkg/money"
)

// Settings is the tenant-owned checkout fee policy. Amounts are always kobo.
type Settings struct {
	FixedBankChargeMinor int64
	Currency             string
	UpdatedAt            time.Time
	UpdatedBy            string
}

type SettingsStore interface {
	LoadSettings(context.Context, string) (Settings, error)
	SaveSettings(context.Context, string, MutationActor, Settings) error
}

type SettingsService struct {
	store  SettingsStore
	stepUp StepUpVerifier
}

func NewSettingsService(store SettingsStore, stepUp StepUpVerifier) (*SettingsService, error) {
	if store == nil || stepUp == nil {
		return nil, ErrUnavailable
	}
	return &SettingsService{store: store, stepUp: stepUp}, nil
}

func (s *SettingsService) Get(ctx context.Context, tenantID string) (Settings, error) {
	if s == nil || s.store == nil || !validUUID(tenantID) {
		return Settings{}, ErrUnavailable
	}
	return s.store.LoadSettings(ctx, tenantID)
}

func (s *SettingsService) Update(ctx context.Context, principal auth.Principal, actor MutationActor, decimal, password, mfaCode string) (Settings, error) {
	if s == nil || s.store == nil || s.stepUp == nil || !validUUID(principal.TenantID) || !validUUID(principal.UserID) {
		return Settings{}, ErrInvalidClear
	}
	amount, err := money.ParseMinor(strings.TrimSpace(decimal), "NGN")
	if err != nil || amount.Minor() < 0 {
		return Settings{}, ErrInvalidClear
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return Settings{}, ErrStepUpFailed
	}
	settings := Settings{FixedBankChargeMinor: amount.Minor(), Currency: "NGN", UpdatedBy: principal.UserID}
	actor.UserID = principal.UserID
	if err := s.store.SaveSettings(ctx, principal.TenantID, actor, settings); err != nil {
		return Settings{}, err
	}
	return settings, nil
}
