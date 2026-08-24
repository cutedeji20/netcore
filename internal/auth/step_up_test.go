package auth

import (
	"context"
	"errors"
	"testing"
)

func TestVerifyStepUpRequiresCurrentPasswordAndFreshStaffMFA(t *testing.T) {
	// This fails if a stale browser session alone can rotate an integration
	// credential, or if a bad password consumes a valid TOTP code.
	service, store := newTestService(t)
	verifier := &testMFAVerifier{}
	if err := service.RequireMFA(verifier); err != nil {
		t.Fatal(err)
	}
	principal := Principal{TenantID: store.tenantID, UserID: store.user.ID, Email: store.user.Email}

	if err := service.VerifyStepUp(context.Background(), StepUpInput{Principal: principal, Password: "wrong password", MFACode: "123456"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v, want invalid credentials", err)
	}
	if verifier.calls != 0 {
		t.Fatalf("wrong password consumed MFA verifier %d times", verifier.calls)
	}

	if err := service.VerifyStepUp(context.Background(), StepUpInput{Principal: principal, Password: "correct password", MFACode: "654321"}); err != nil {
		t.Fatalf("VerifyStepUp: %v", err)
	}
	if verifier.calls != 1 || verifier.code != "654321" {
		t.Fatalf("MFA verifier calls=%d code=%q", verifier.calls, verifier.code)
	}
}

func TestVerifyStepUpRejectsCustomerAndPrincipalMismatch(t *testing.T) {
	// This fails if a customer account or a forged principal can satisfy the
	// credential-rotation step-up without a currently enrolled staff user.
	service, store := newTestService(t)
	if err := service.RequireMFA(&testMFAVerifier{}); err != nil {
		t.Fatal(err)
	}
	store.user.RequiresMFA = false
	store.user.EmailVerified = true
	principal := Principal{TenantID: store.tenantID, UserID: store.user.ID, Email: store.user.Email}
	if err := service.VerifyStepUp(context.Background(), StepUpInput{Principal: principal, Password: "correct password", MFACode: "123456"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("customer step-up = %v, want invalid credentials", err)
	}

	store.user.RequiresMFA = true
	principal.UserID = "44444444-4444-4444-8444-444444444444"
	if err := service.VerifyStepUp(context.Background(), StepUpInput{Principal: principal, Password: "correct password", MFACode: "123456"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("mismatched principal step-up = %v, want invalid credentials", err)
	}
}
