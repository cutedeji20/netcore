package vouchers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

var ErrStaffVoucherDenied = errors.New("vouchers: staff mutation denied")

type StepUpVerifier interface {
	VerifyStepUp(context.Context, auth.StepUpInput) error
}
type Revoker interface {
	RevokeVoucher(context.Context, string, string, string) error
}

type StaffService struct {
	issuer  *Issuer
	revoker Revoker
	stepUp  StepUpVerifier
}

func NewStaffService(issuer *Issuer, revoker Revoker, stepUp StepUpVerifier) (*StaffService, error) {
	if issuer == nil || revoker == nil || stepUp == nil {
		return nil, ErrStaffVoucherDenied
	}
	return &StaffService{issuer: issuer, revoker: revoker, stepUp: stepUp}, nil
}

func (s *StaffService) Issue(ctx context.Context, principal auth.Principal, planID string, quantity int, expiresAt *time.Time, reason, password, mfaCode string) (Batch, error) {
	if s == nil || !validUUID(principal.TenantID) || !validUUID(principal.UserID) || !validUUID(planID) || !principal.HasPermission("team.write") || !principal.HasPermission("voucher.write") || quantity < 1 || quantity > 100 || strings.TrimSpace(reason) == "" || len(reason) > 500 || (expiresAt != nil && !expiresAt.After(time.Now())) {
		return Batch{}, ErrStaffVoucherDenied
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return Batch{}, ErrStaffVoucherDenied
	}
	return s.issuer.IssueBatch(ctx, principal.TenantID, IssueRequest{PlanID: planID, Quantity: quantity, IssuerUserID: principal.UserID, Reason: strings.TrimSpace(reason), ExpiresAt: expiresAt})
}

func (s *StaffService) Revoke(ctx context.Context, principal auth.Principal, voucherID, password, mfaCode string) error {
	if s == nil || !validUUID(principal.TenantID) || !validUUID(principal.UserID) || !validUUID(voucherID) || !principal.HasPermission("team.write") || !principal.HasPermission("voucher.write") {
		return ErrStaffVoucherDenied
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return ErrStaffVoucherDenied
	}
	return s.revoker.RevokeVoucher(ctx, principal.TenantID, voucherID, principal.UserID)
}
