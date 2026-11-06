package vouchers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

var ErrStaffVoucherDenied = errors.New("vouchers: staff mutation denied")
var ErrVoucherHasOpenSession = errors.New("vouchers: active network session must be disconnected before revocation")
var ErrBatchVoidNotAllowed = errors.New("vouchers: batch cannot be voided")

type StepUpVerifier interface {
	VerifyStepUp(context.Context, auth.StepUpInput) error
}
type Revoker interface {
	RevokeVoucher(context.Context, string, string, string) error
}
type BatchVoider interface {
	VoidUnusedBatches(context.Context, string, string, []string) (int, error)
}

type StaffService struct {
	issuer  *Issuer
	revoker Revoker
	voider  BatchVoider
	stepUp  StepUpVerifier
}

func NewStaffService(issuer *Issuer, revoker Revoker, stepUp StepUpVerifier) (*StaffService, error) {
	if issuer == nil || revoker == nil || stepUp == nil {
		return nil, ErrStaffVoucherDenied
	}
	voider, _ := revoker.(BatchVoider)
	return &StaffService{issuer: issuer, revoker: revoker, voider: voider, stepUp: stepUp}, nil
}

// VoidUnusedBatches removes accidental staff-issued inventory from normal
// operations without erasing evidence. It only revokes batches whose every
// code remains UNUSED; paid customer orders and any redeemed/mixed batch are
// rejected by the store atomically.
func (s *StaffService) VoidUnusedBatches(ctx context.Context, principal auth.Principal, batchIDs []string, password, mfaCode string) (int, error) {
	if s == nil || s.voider == nil || !validUUID(principal.TenantID) || !validUUID(principal.UserID) || !principal.HasPermission("team.write") || !principal.HasPermission("voucher.write") || len(batchIDs) < 1 || len(batchIDs) > 50 {
		return 0, ErrStaffVoucherDenied
	}
	seen := make(map[string]struct{}, len(batchIDs))
	for _, batchID := range batchIDs {
		if !validUUID(batchID) {
			return 0, ErrStaffVoucherDenied
		}
		if _, exists := seen[batchID]; exists {
			return 0, ErrStaffVoucherDenied
		}
		seen[batchID] = struct{}{}
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return 0, ErrStaffVoucherDenied
	}
	return s.voider.VoidUnusedBatches(ctx, principal.TenantID, principal.UserID, batchIDs)
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

// Reveal returns a batch's codes to its active buyer or issuer. An authenticated
// session and the ownership check performed by Issuer.RevealBatch are sufficient:
// users must not have to enter their password and TOTP again just to retrieve a
// purchase they already own.
func (s *StaffService) Reveal(ctx context.Context, principal auth.Principal, batchID string) ([]IssuedCode, error) {
	if s == nil || !validUUID(principal.TenantID) || !validUUID(principal.UserID) || !validUUID(batchID) {
		return nil, ErrCodeUnavailable
	}
	return s.issuer.RevealBatch(ctx, principal.TenantID, principal.UserID, batchID)
}

// PrintableBatch permits an active owner/issuer or staff member with
// voucher.read permission to create a cut-ready copy. The store repeats the
// authorization check in the tenant transaction and audits every print.
func (s *StaffService) PrintableBatch(ctx context.Context, principal auth.Principal, batchID string) (PrintableBatch, error) {
	if s == nil || !validUUID(principal.TenantID) || !validUUID(principal.UserID) || !validUUID(batchID) {
		return PrintableBatch{}, ErrCodeUnavailable
	}
	return s.issuer.PrintableBatch(ctx, principal.TenantID, principal.UserID, batchID)
}
