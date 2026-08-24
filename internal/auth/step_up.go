package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// StepUpInput contains the current browser principal and the two fresh factors
// required before a staff member may alter an integration credential.
type StepUpInput struct {
	Principal Principal
	Password  string
	MFACode   string
}

// VerifyStepUp rechecks the session principal against the current user record,
// then verifies the current password before consuming a TOTP code. It never
// creates, extends, or otherwise changes a browser session.
func (s *Service) VerifyStepUp(ctx context.Context, in StepUpInput) error {
	if s == nil || s.store == nil || strings.TrimSpace(in.Principal.TenantID) == "" || strings.TrimSpace(in.Principal.UserID) == "" || strings.TrimSpace(in.Principal.Email) == "" || in.Password == "" {
		return ErrInvalidCredentials
	}
	if s.mfa == nil {
		return ErrMFAUnavailable
	}

	user, found, err := s.store.FindUser(ctx, in.Principal.TenantID, NormalizeLoginIdentifier(in.Principal.Email))
	if err != nil {
		return fmt.Errorf("auth: find step-up user: %w", err)
	}
	if !found || user.ID != in.Principal.UserID || user.TenantID != in.Principal.TenantID || user.Status != "ACTIVE" || !user.RequiresMFA || user.PasswordHash == "" {
		return ErrInvalidCredentials
	}
	matched, err := s.hasher.Verify(in.Password, user.PasswordHash)
	if err != nil || !matched {
		return ErrInvalidCredentials
	}
	if err := s.mfa.VerifyTOTP(ctx, user.TenantID, user.ID, strings.TrimSpace(in.MFACode)); err != nil {
		if errors.Is(err, ErrInvalidMFA) {
			return ErrInvalidCredentials
		}
		return fmt.Errorf("auth: verify step-up MFA: %w", err)
	}
	return nil
}
