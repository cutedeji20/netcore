package workspace

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

var (
	ErrResetDisabled = errors.New("workspace: test-data reset is disabled")
	ErrInvalidReset  = errors.New("workspace: invalid test-data reset request")
	ErrResetBlocked  = errors.New("workspace: test-data reset is blocked")
	ErrStepUpFailed  = errors.New("workspace: test-data reset step-up failed")
)

// TestDataResetPreview is deliberately count-only. It never exposes customer,
// payment, device, session, or provider-identifying data in Settings.
type TestDataResetPreview struct {
	Customers          int64 `json:"customers"`
	CustomerUsers      int64 `json:"customer_users"`
	Devices            int64 `json:"devices"`
	Subscriptions      int64 `json:"subscriptions"`
	SubscriptionEvents int64 `json:"subscription_events"`
	Sessions           int64 `json:"sessions"`
	AccountingRecords  int64 `json:"accounting_records"`
	Payments           int64 `json:"payments"`
	Invoices           int64 `json:"invoices"`
	Vouchers           int64 `json:"vouchers"`
	LedgerEntries      int64 `json:"ledger_entries"`
	OutboxEvents       int64 `json:"outbox_events"`
	ActiveSessions     int64 `json:"active_sessions"`
	ActiveReservations int64 `json:"active_reservations"`
	UnpublishedOutbox  int64 `json:"unpublished_outbox"`
}

func (p TestDataResetPreview) Blocked() bool {
	return p.ActiveSessions > 0 || p.ActiveReservations > 0 || p.UnpublishedOutbox > 0
}

type TestDataResetInput struct {
	Principal       auth.Principal
	Password        string
	MFACode         string
	Confirmation    string
	BackupReference string
	Reason          string
}

type TestDataResetResult struct {
	RunID     string               `json:"run_id"`
	Completed time.Time            `json:"completed_at"`
	Removed   TestDataResetPreview `json:"removed"`
}

type TestDataResetStore interface {
	PreviewTestDataReset(context.Context, string) (TestDataResetPreview, error)
	ExecuteTestDataReset(context.Context, string, string, string, string) (TestDataResetResult, error)
}

type StepUpVerifier interface {
	VerifyStepUp(context.Context, auth.StepUpInput) error
}

// TestDataResetService owns the high-risk action boundary. The database
// function performs a second permission and tenant check, so browser and API
// authorization are not the only controls protecting the reset.
type TestDataResetService struct {
	store  TestDataResetStore
	stepUp StepUpVerifier
}

func NewTestDataResetService(store TestDataResetStore, stepUp StepUpVerifier) (*TestDataResetService, error) {
	if store == nil || stepUp == nil {
		return nil, ErrUnavailable
	}
	return &TestDataResetService{store: store, stepUp: stepUp}, nil
}

func (s *TestDataResetService) Preview(ctx context.Context, principal auth.Principal) (TestDataResetPreview, error) {
	if s == nil || s.store == nil || !principal.HasPermission("tenant.test_data_reset") || !validUUID(principal.TenantID) {
		return TestDataResetPreview{}, ErrInvalidReset
	}
	return s.store.PreviewTestDataReset(ctx, principal.TenantID)
}

func (s *TestDataResetService) Execute(ctx context.Context, in TestDataResetInput, tenantSlug string) (TestDataResetResult, error) {
	if s == nil || s.store == nil || s.stepUp == nil || !in.Principal.HasPermission("tenant.test_data_reset") || !validUUID(in.Principal.TenantID) || !validUUID(in.Principal.UserID) {
		return TestDataResetResult{}, ErrInvalidReset
	}
	backup := strings.TrimSpace(in.BackupReference)
	reason := strings.TrimSpace(in.Reason)
	if backup == "" || len(backup) > 300 || reason == "" || len(reason) > 240 || in.Password == "" || strings.TrimSpace(in.MFACode) == "" || strings.TrimSpace(tenantSlug) == "" || strings.TrimSpace(in.Confirmation) != "RESET "+tenantSlug {
		return TestDataResetResult{}, ErrInvalidReset
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: in.Principal, Password: in.Password, MFACode: in.MFACode}); err != nil {
		return TestDataResetResult{}, ErrStepUpFailed
	}
	preview, err := s.store.PreviewTestDataReset(ctx, in.Principal.TenantID)
	if err != nil {
		return TestDataResetResult{}, err
	}
	if preview.Blocked() {
		return TestDataResetResult{}, ErrResetBlocked
	}
	return s.store.ExecuteTestDataReset(ctx, in.Principal.TenantID, in.Principal.UserID, backup, reason)
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
