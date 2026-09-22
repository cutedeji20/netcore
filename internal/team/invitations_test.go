package team

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
	"github.com/netcore-isp/netcore/pkg/crypto/totp"
)

func TestMFASetupIncludesLocalQRCode(t *testing.T) {
	setup := mfaSetup("ops@example.test", "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP")
	field := reflect.ValueOf(setup).FieldByName("QRCode")
	if !field.IsValid() || !strings.HasPrefix(field.String(), "data:image/png;base64,") {
		t.Fatal("MFA setup must include a local PNG data URI")
	}
}

// This catches a regression that puts invitation credentials in a query string,
// where they would be sent in logs and Referer headers.
func TestInviteRequiresStepUpAndUsesFragmentToken(t *testing.T) {
	sender := &recordingSender{}
	service := newInvitationService(t, acceptingStepUp{}, sender)
	_, err := service.Invite(context.Background(), InviteInput{
		Principal: administrator(), Email: "ops@example.test", Role: RoleOperations,
		Password: "correct password", MFACode: "123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sender.url, "?token=") || !strings.Contains(sender.url, "#token=") {
		t.Fatal("token leaked into HTTP URL")
	}
}

func TestRedeemInvitationOnce(t *testing.T) {
	store := &memoryInvitationStore{}
	sender := &recordingSender{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, sender)
	if _, err := service.Invite(context.Background(), InviteInput{Principal: administrator(), Email: "ops@example.test", Role: RoleOperations, Password: "correct password", MFACode: "123456"}); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(strings.Split(sender.url, "#")[1], "token=")
	setup, err := service.PrepareAcceptance(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(setup.ManualKey, time.Now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	in := CompleteInvitationInput{Token: token, Password: "a long enough invitation password", MFACode: code}
	if err := service.CompleteAcceptance(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.OpenTOTPSecret(context.Background(), testInvitationWrapper{}, store.invitation.TenantID, "user-mfa-totp", store.enrolledUserID, store.enrolledMFA); err != nil {
		t.Fatalf("enrolled invitation MFA cannot be opened for the user: %v", err)
	}
	if err := service.CompleteAcceptance(context.Background(), in); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("second redemption = %v", err)
	}
}

func TestCompletedRecoveryEnrollsUserScopedMFA(t *testing.T) {
	token, digest, err := newInvitationToken()
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryInvitationStore{recovery: MFARecovery{ID: "55555555-5555-4555-8555-555555555555", TenantID: teamTestTenantID, UserID: "33333333-3333-4333-8333-333333333333", Email: "staff@example.test", Status: "PENDING", ExpiresAt: time.Now().Add(time.Hour)}, recoveryDigest: digest}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, &recordingSender{})
	setup, err := service.PrepareMFARecovery(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(setup.ManualKey, time.Now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteMFARecovery(context.Background(), CompleteMFARecoveryInput{Token: token, MFACode: code}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.OpenTOTPSecret(context.Background(), testInvitationWrapper{}, store.recovery.TenantID, "user-mfa-totp", store.recovery.UserID, store.enrolledMFA); err != nil {
		t.Fatalf("enrolled recovery MFA cannot be opened for the user: %v", err)
	}
}

func TestCompleteInvitationRequiresSixteenCharacterPassword(t *testing.T) {
	store := &memoryInvitationStore{}
	sender := &recordingSender{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, sender)
	if _, err := service.Invite(context.Background(), InviteInput{Principal: administrator(), Email: "ops@example.test", Role: RoleOperations, Password: "correct password", MFACode: "123456"}); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(strings.Split(sender.url, "#")[1], "token=")
	setup, err := service.PrepareAcceptance(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(setup.ManualKey, time.Now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteAcceptance(context.Background(), CompleteInvitationInput{Token: token, Password: "too short", MFACode: code}); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("short password = %v", err)
	}
}

func TestCannotDeactivateFinalAdministrator(t *testing.T) {
	store := &memoryInvitationStore{deactivateErr: ErrLastAdministrator}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, &recordingSender{})
	err := service.Deactivate(context.Background(), DeactivateInput{Principal: administrator(), UserID: "33333333-3333-4333-8333-333333333333", Password: "correct password", MFACode: "123456"})
	if !errors.Is(err, ErrLastAdministrator) {
		t.Fatalf("Deactivate = %v", err)
	}
}

func TestInviteDeliveryFailureLeavesNoRedeemableDigest(t *testing.T) {
	store := &memoryInvitationStore{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, &recordingSender{err: errors.New("delivery unavailable")})
	_, err := service.Invite(context.Background(), InviteInput{Principal: administrator(), Email: "ops@example.test", Role: RoleOperations, Password: "correct password", MFACode: "123456"})
	if !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("Invite = %v", err)
	}
	if store.invitation.Status == "PENDING" {
		t.Fatal("failed delivery left a redeemable invitation")
	}
}

func TestCompleteInvitationRejectsWrongOTPAndRevokedToken(t *testing.T) {
	store := &memoryInvitationStore{}
	sender := &recordingSender{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, sender)
	if _, err := service.Invite(context.Background(), InviteInput{Principal: administrator(), Email: "ops@example.test", Role: RoleOperations, Password: "correct password", MFACode: "123456"}); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(strings.Split(sender.url, "#")[1], "token=")
	if _, err := service.PrepareAcceptance(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteAcceptance(context.Background(), CompleteInvitationInput{Token: token, Password: "a long enough invitation password", MFACode: "000000"}); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("wrong OTP = %v", err)
	}
	store.invitation.Status = "REVOKED"
	if _, err := service.PrepareAcceptance(context.Background(), token); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("revoked prepare = %v", err)
	}
}

func TestInviteMapsActiveStaffConflict(t *testing.T) {
	store := &memoryInvitationStore{createErr: ErrStaffConflict}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, &recordingSender{})
	_, err := service.Invite(context.Background(), InviteInput{Principal: administrator(), Email: "ops@example.test", Role: RoleOperations, Password: "correct password", MFACode: "123456"})
	if !errors.Is(err, ErrStaffConflict) {
		t.Fatalf("Invite = %v", err)
	}
}

func TestResendInvalidatesOldDigest(t *testing.T) {
	store := &memoryInvitationStore{}
	sender := &recordingSender{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, sender)
	if _, err := service.Invite(context.Background(), InviteInput{Principal: administrator(), Email: "ops@example.test", Role: RoleOperations, Password: "correct password", MFACode: "123456"}); err != nil {
		t.Fatal(err)
	}
	oldToken := strings.TrimPrefix(strings.Split(sender.url, "#")[1], "token=")
	oldID := store.invitation.ID
	if _, err := service.ResendWithResult(context.Background(), ResendInput{Principal: administrator(), InvitationID: oldID, Password: "correct password", MFACode: "123456"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareAcceptance(context.Background(), oldToken); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("old token prepare = %v", err)
	}
}

func TestExpiredInvitationIsInvalid(t *testing.T) {
	store := &memoryInvitationStore{}
	sender := &recordingSender{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, sender)
	if _, err := service.Invite(context.Background(), InviteInput{Principal: administrator(), Email: "ops@example.test", Role: RoleOperations, Password: "correct password", MFACode: "123456"}); err != nil {
		t.Fatal(err)
	}
	store.invitation.ExpiresAt = time.Now().Add(-time.Minute)
	token := strings.TrimPrefix(strings.Split(sender.url, "#")[1], "token=")
	if _, err := service.PrepareAcceptance(context.Background(), token); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("expired prepare=%v", err)
	}
}

func TestCannotDemoteFinalAdministrator(t *testing.T) {
	store := &memoryInvitationStore{roleErr: ErrLastAdministrator}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, &recordingSender{})
	err := service.ChangeRole(context.Background(), RoleChangeInput{Principal: administrator(), UserID: "33333333-3333-4333-8333-333333333333", Role: RoleOperations, Password: "correct password", MFACode: "123456"})
	if !errors.Is(err, ErrLastAdministrator) {
		t.Fatalf("ChangeRole=%v", err)
	}
}

func TestTenantMismatchedTargetIsRejected(t *testing.T) {
	store := &memoryInvitationStore{deactivateErr: ErrInvitationInvalid}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, &recordingSender{})
	err := service.Deactivate(context.Background(), DeactivateInput{Principal: administrator(), UserID: "33333333-3333-4333-8333-333333333333", Password: "correct password", MFACode: "123456"})
	if !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("Deactivate=%v", err)
	}
}

func TestSuccessfulDeactivationInvalidatesAllTargetSessions(t *testing.T) {
	store := &memoryInvitationStore{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, &recordingSender{})
	err := service.Deactivate(context.Background(), DeactivateInput{Principal: administrator(), UserID: "33333333-3333-4333-8333-333333333333", Password: "correct password", MFACode: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	if !store.sessionsInvalidated {
		t.Fatal("successful deactivation did not invalidate target sessions")
	}
}

func TestResetStaffPasswordRequiresStepUpAndInvalidatesSessions(t *testing.T) {
	store := &memoryInvitationStore{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, &recordingSender{})
	err := service.ResetStaffPassword(context.Background(), PasswordResetInput{
		Principal: administrator(), UserID: "33333333-3333-4333-8333-333333333333",
		NewPassword: "a sufficiently long replacement password", Password: "correct password", MFACode: "123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !store.sessionsInvalidated || store.resetPasswordHash == "" {
		t.Fatal("password reset did not invalidate sessions and persist a hash")
	}
}

type recordingSender struct {
	url string
	err error
}

func (s *recordingSender) SendStaffInvitation(_ context.Context, _ string, inviteURL string, _ time.Time) error {
	s.url = inviteURL
	return s.err
}
func (s *recordingSender) SendStaffMFARecoveryForTenant(_ context.Context, _, _, recoveryURL string, _ time.Time, _ string) error {
	s.url = recoveryURL
	return s.err
}

type acceptingStepUp struct{}

func (acceptingStepUp) VerifyStepUp(context.Context, auth.StepUpInput) error { return nil }

func administrator() auth.Principal {
	return auth.Principal{TenantID: teamTestTenantID, UserID: "22222222-2222-4222-8222-222222222222", Permissions: map[string]struct{}{"team.write": {}}}
}

func newInvitationService(t *testing.T, stepUp StepUpVerifier, sender StaffInvitationSender) *Service {
	return newInvitationServiceWithStore(t, &memoryInvitationStore{}, stepUp, sender)
}

func newInvitationServiceWithStore(t *testing.T, store *memoryInvitationStore, stepUp StepUpVerifier, sender StaffInvitationSender) *Service {
	t.Helper()
	hasher, err := argon2id.New(argon2id.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, testInvitationWrapper{}, stepUp, sender, hasher, "https://app.example.test/invitations/accept")
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type testInvitationWrapper struct{}

func (testInvitationWrapper) Wrap(_ context.Context, value []byte) (envelope.WrappedKey, error) {
	return envelope.WrappedKey{Ciphertext: append([]byte(nil), value...), KeyID: "test"}, nil
}
func (testInvitationWrapper) Unwrap(_ context.Context, value []byte, keyID string) ([]byte, error) {
	if keyID != "test" {
		return nil, errors.New("wrong key")
	}
	return append([]byte(nil), value...), nil
}

type memoryInvitationStore struct {
	invitation          Invitation
	digest              []byte
	deactivateErr       error
	createErr           error
	archived            Invitation
	archivedDigest      []byte
	roleErr             error
	sessionsInvalidated bool
	resetPasswordHash   string
	recovery            MFARecovery
	recoveryDigest      []byte
	enrolledMFA         auth.MFASecretEnvelope
	enrolledUserID      string
}

func (s *memoryInvitationStore) CreateInvitation(_ context.Context, invitation Invitation, digest []byte) (Invitation, error) {
	if s.createErr != nil {
		return Invitation{}, s.createErr
	}
	if s.invitation.ID != "" {
		s.archived = s.invitation
		s.archivedDigest = append([]byte(nil), s.digest...)
		invitation.ID = "44444444-4444-4444-8444-444444444444"
	} else {
		invitation.ID = "33333333-3333-4333-8333-333333333333"
	}
	s.invitation, s.digest = invitation, append([]byte(nil), digest...)
	return invitation, nil
}
func (s *memoryInvitationStore) ListInvitations(_ context.Context, tenantID string) ([]Invitation, error) {
	if tenantID != s.invitation.TenantID || (s.invitation.Status != "PENDING" && s.invitation.Status != "DELIVERY_PENDING") {
		return []Invitation{}, nil
	}
	return []Invitation{s.invitation}, nil
}
func (s *memoryInvitationStore) MarkInvitationDelivered(_ context.Context, tenantID, invitationID, _ string) error {
	if tenantID != s.invitation.TenantID || invitationID != s.invitation.ID || s.invitation.Status != "DELIVERY_PENDING" {
		return ErrInvitationInvalid
	}
	s.invitation.Status = "PENDING"
	return nil
}
func (s *memoryInvitationStore) FindInvitation(_ context.Context, tenantID, invitationID string) (Invitation, bool, error) {
	return s.invitation, tenantID == s.invitation.TenantID && invitationID == s.invitation.ID, nil
}
func (s *memoryInvitationStore) FindInvitationByDigest(_ context.Context, digest []byte) (Invitation, bool, error) {
	if string(digest) == string(s.archivedDigest) {
		return s.archived, true, nil
	}
	return s.invitation, string(digest) == string(s.digest), nil
}
func (s *memoryInvitationStore) CreateOrReuseInvitationMFA(_ context.Context, invitation Invitation, _ []byte, mfa auth.MFASecretEnvelope) (auth.MFASecretEnvelope, error) {
	if presentEnvelope(s.invitation.MFA) {
		return s.invitation.MFA, nil
	}
	s.invitation.MFA = mfa
	return mfa, nil
}
func (s *memoryInvitationStore) CompleteInvitation(_ context.Context, invitation Invitation, userID, _ string, mfa auth.MFASecretEnvelope, _ int64) error {
	if invitation.ID != s.invitation.ID || s.invitation.Status != "PENDING" {
		return ErrInvitationInvalid
	}
	s.invitation.Status = "REDEEMED"
	s.enrolledUserID = userID
	s.enrolledMFA = mfa
	return nil
}
func (s *memoryInvitationStore) RevokeInvitation(_ context.Context, _ string, id string, _ string) error {
	if id == s.invitation.ID {
		s.invitation.Status = "REVOKED"
	} else if id == s.archived.ID {
		s.archived.Status = "REVOKED"
	} else {
		return ErrInvitationInvalid
	}
	return nil
}
func (s *memoryInvitationStore) ChangeStaffRole(context.Context, string, string, string, BuiltInRole) error {
	if s.roleErr == nil {
		s.sessionsInvalidated = true
	}
	return s.roleErr
}
func (s *memoryInvitationStore) DeactivateStaff(context.Context, string, string, string) error {
	if s.deactivateErr == nil {
		s.sessionsInvalidated = true
	}
	return s.deactivateErr
}

func (s *memoryInvitationStore) ReactivateStaff(context.Context, string, string, string) error {
	return nil
}

func (s *memoryInvitationStore) CreateMFARecovery(_ context.Context, tenantID, actorID, targetID string, _ []byte, expiresAt time.Time) (MFARecovery, error) {
	return MFARecovery{ID: "55555555-5555-4555-8555-555555555555", TenantID: tenantID, UserID: targetID, Email: "staff@example.test", Status: "PENDING", CreatedBy: actorID, ExpiresAt: expiresAt}, nil
}
func (s *memoryInvitationStore) ActivateStaffPasswordReset(_ context.Context, _ MFARecovery, passwordHash string) error {
	s.resetPasswordHash = passwordHash
	s.sessionsInvalidated = true
	return nil
}
func (s *memoryInvitationStore) RevokeMFARecovery(context.Context, string, string, string) error {
	return nil
}
func (s *memoryInvitationStore) FindMFARecoveryByDigest(_ context.Context, digest []byte) (MFARecovery, bool, error) {
	return s.recovery, string(digest) == string(s.recoveryDigest), nil
}
func (s *memoryInvitationStore) CreateOrReuseMFARecoveryMFA(_ context.Context, r MFARecovery, _ []byte, m auth.MFASecretEnvelope) (auth.MFASecretEnvelope, error) {
	s.recovery.MFA = m
	return m, nil
}
func (s *memoryInvitationStore) CompleteMFARecovery(_ context.Context, _ MFARecovery, m auth.MFASecretEnvelope, _ int64) error {
	s.enrolledMFA = m
	s.recovery.Status = "REDEEMED"
	return nil
}
