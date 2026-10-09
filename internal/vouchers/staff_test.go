package vouchers

import (
	"context"
	"errors"
	"testing"

	"github.com/netcore-isp/netcore/internal/auth"
)

type staffStepUpStub struct{ err error }

func (s staffStepUpStub) VerifyStepUp(context.Context, auth.StepUpInput) error { return s.err }

type revokerStub struct{ calls int }

func (s *revokerStub) RevokeVoucher(context.Context, string, string, string) error {
	s.calls++
	return nil
}

func TestStaffVoucherIssueRequiresAdminPermissionReasonAndStepUp(t *testing.T) {
	store := &issuingStoreStub{}
	issuer, _ := NewIssuer(store, voucherTestWrapper{}, "https://vault.test/keys/voucher/1")
	revoker := &revokerStub{}
	service, _ := NewStaffService(issuer, revoker, staffStepUpStub{})
	principal := auth.Principal{TenantID: voucherTestTenantID, UserID: "22222222-2222-4222-8222-222222222222", Permissions: map[string]struct{}{"voucher.write": {}}}
	_, err := service.Issue(context.Background(), principal, "33333333-3333-4333-8333-333333333333", 1, nil, "sale", "password", "123456")
	if !errors.Is(err, ErrStaffVoucherDenied) || store.saved != 0 {
		t.Fatalf("non-admin err=%v saved=%d", err, store.saved)
	}
	principal.Permissions["team.write"] = struct{}{}
	_, err = service.Issue(context.Background(), principal, "33333333-3333-4333-8333-333333333333", 1, nil, "", "password", "123456")
	if !errors.Is(err, ErrStaffVoucherDenied) || store.saved != 0 {
		t.Fatalf("missing reason err=%v saved=%d", err, store.saved)
	}
	service.stepUp = staffStepUpStub{err: errors.New("no")}
	_, err = service.Issue(context.Background(), principal, "33333333-3333-4333-8333-333333333333", 1, nil, "sale", "password", "123456")
	if !errors.Is(err, ErrStaffVoucherDenied) || store.saved != 0 {
		t.Fatalf("failed step-up err=%v saved=%d", err, store.saved)
	}
	service.stepUp = staffStepUpStub{}
	batch, err := service.Issue(context.Background(), principal, "33333333-3333-4333-8333-333333333333", 1, nil, "sale", "password", "123456")
	if err != nil || batch.Issued != 1 || store.saved != 1 {
		t.Fatalf("batch=%+v err=%v saved=%d", batch, err, store.saved)
	}
}

func TestStaffVoucherRevokeRequiresStepUp(t *testing.T) {
	issuer, _ := NewIssuer(&issuingStoreStub{}, voucherTestWrapper{}, "https://vault.test/keys/voucher/1")
	revoker := &revokerStub{}
	service, _ := NewStaffService(issuer, revoker, staffStepUpStub{err: errors.New("no")})
	p := auth.Principal{TenantID: voucherTestTenantID, UserID: "22222222-2222-4222-8222-222222222222", Permissions: map[string]struct{}{"team.write": {}, "voucher.write": {}}}
	err := service.Revoke(context.Background(), p, "33333333-3333-4333-8333-333333333333", "password", "123456")
	if !errors.Is(err, ErrStaffVoucherDenied) || revoker.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, revoker.calls)
	}
}

func TestRevealRequiresStepUpAndKeepsBuyerIdentity(t *testing.T) {
	store := &issuingStoreStub{}
	issuer, _ := NewIssuer(store, voucherTestWrapper{}, "https://vault.test/keys/voucher/1")
	service, _ := NewStaffService(issuer, &revokerStub{}, staffStepUpStub{err: errors.New("invalid code")})
	p := auth.Principal{TenantID: voucherTestTenantID, UserID: "22222222-2222-4222-8222-222222222222"}
	batchID := "33333333-3333-4333-8333-333333333333"
	if _, err := service.Reveal(context.Background(), p, batchID, "password", "000000"); !errors.Is(err, ErrCodeUnavailable) || store.revealed != 0 {
		t.Fatalf("failed step-up reached store: %v", err)
	}
	service.stepUp = staffStepUpStub{}
	codes, err := service.Reveal(context.Background(), p, batchID, "password", "123456")
	if err != nil || len(codes) != 1 || store.revealed != 1 || store.revealTenant != p.TenantID || store.revealActor != p.UserID || store.revealBatch != batchID {
		t.Fatalf("reveal=%+v err=%v store=%+v", codes, err, store)
	}
}
