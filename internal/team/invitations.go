package team

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
	"github.com/netcore-isp/netcore/pkg/crypto/totp"
)

var (
	ErrInvitationInvalid = errors.New("team: invitation is invalid")
	ErrStepUpFailed      = errors.New("team: step-up verification failed")
	ErrStaffConflict     = errors.New("team: staff member already exists")
	ErrLastAdministrator = errors.New("team: final administrator cannot be changed")
	ErrStoreUnavailable  = errors.New("team: invitation store unavailable")
)

const invitationLifetime = 24 * time.Hour

// StaffInvitationSender delivers a URL containing the credential only in its
// fragment. Implementations must not log the URL or the delivery response.
type StaffInvitationSender interface {
	SendStaffInvitation(context.Context, string, string, time.Time) error
}
type TenantStaffInvitationSender interface {
	SendStaffInvitationForTenant(context.Context, string, string, string, time.Time) error
}
type invitationIDSender interface {
	SendStaffInvitationWithID(context.Context, string, string, time.Time, string) error
}
type tenantInvitationIDSender interface {
	SendStaffInvitationForTenantWithID(context.Context, string, string, string, time.Time, string) error
}

type StepUpVerifier interface {
	VerifyStepUp(context.Context, auth.StepUpInput) error
}

type passwordHasher interface{ Hash(string) (string, error) }

type Invitation struct {
	ID        string
	TenantID  string
	Email     string
	Role      BuiltInRole
	Status    string
	ExpiresAt time.Time
	CreatedBy string
	MFA       auth.MFASecretEnvelope
}

type InviteInput struct {
	Principal auth.Principal
	Email     string
	Role      BuiltInRole
	Password  string
	MFACode   string
}
type ResendInput struct {
	Principal                       auth.Principal
	InvitationID, Password, MFACode string
}
type RevokeInput struct {
	Principal                       auth.Principal
	InvitationID, Password, MFACode string
}
type RoleChangeInput struct {
	Principal         auth.Principal
	UserID            string
	Role              BuiltInRole
	Password, MFACode string
}
type DeactivateInput struct {
	Principal         auth.Principal
	UserID            string
	Password, MFACode string
}
type MFASetup struct {
	URI       string `json:"uri"`
	ManualKey string `json:"manual_key"`
}
type CompleteInvitationInput struct {
	Token    string `json:"token"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code"`
}

// InvitationStore defines atomic tenant-scoped mutation boundaries. Methods
// that receive a tenant must execute inside a tenant RLS transaction and keep
// explicit tenant predicates in each query.
type InvitationStore interface {
	CreateInvitation(context.Context, Invitation, []byte) (Invitation, error)
	ListInvitations(context.Context, string) ([]Invitation, error)
	MarkInvitationDelivered(context.Context, string, string, string) error
	FindInvitation(context.Context, string, string) (Invitation, bool, error)
	FindInvitationByDigest(context.Context, []byte) (Invitation, bool, error)
	CreateOrReuseInvitationMFA(context.Context, Invitation, []byte, auth.MFASecretEnvelope) (auth.MFASecretEnvelope, error)
	CompleteInvitation(context.Context, Invitation, string, auth.MFASecretEnvelope) error
	RevokeInvitation(context.Context, string, string, string) error
	ChangeStaffRole(context.Context, string, string, string, BuiltInRole) error
	DeactivateStaff(context.Context, string, string, string) error
}

type Service struct {
	store     InvitationStore
	wrapper   envelope.KeyWrapper
	stepUp    StepUpVerifier
	sender    StaffInvitationSender
	hasher    passwordHasher
	inviteURL string
	now       func() time.Time
}

func NewService(store InvitationStore, wrapper envelope.KeyWrapper, stepUp StepUpVerifier, sender StaffInvitationSender, hasher argon2id.Hasher, inviteURL string) (*Service, error) {
	if store == nil || wrapper == nil || stepUp == nil || sender == nil || strings.TrimSpace(inviteURL) == "" {
		return nil, errors.New("team: invitation dependencies are required")
	}
	parsed, err := url.Parse(strings.TrimSpace(inviteURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("team: invitation URL must be an HTTPS URL without query or fragment")
	}
	return &Service{store: store, wrapper: wrapper, stepUp: stepUp, sender: sender, hasher: hasher, inviteURL: strings.TrimSpace(inviteURL), now: time.Now}, nil
}

// ListInvitations returns the safe, durable pending invitation projections for
// a team administrator. Credential material is never part of Invitation's UI
// projection and is not loaded by the backing query.
func (s *Service) ListInvitations(ctx context.Context, principal auth.Principal) ([]Invitation, error) {
	if s == nil || !validMutationPrincipal(principal) {
		return nil, ErrInvitationInvalid
	}
	invitations, err := s.store.ListInvitations(ctx, principal.TenantID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	return invitations, nil
}

func (s *Service) Invite(ctx context.Context, in InviteInput) (Invitation, error) {
	if s == nil || !validMutationPrincipal(in.Principal) || !validRole(in.Role) {
		return Invitation{}, ErrInvitationInvalid
	}
	email, ok := normalizeEmail(in.Email)
	if !ok {
		return Invitation{}, ErrInvitationInvalid
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: in.Principal, Password: in.Password, MFACode: in.MFACode}); err != nil {
		return Invitation{}, ErrStepUpFailed
	}
	raw, digest, err := newInvitationToken()
	if err != nil {
		return Invitation{}, ErrStoreUnavailable
	}
	now := s.now().UTC()
	inv := Invitation{TenantID: in.Principal.TenantID, Email: email, Role: in.Role, Status: "DELIVERY_PENDING", ExpiresAt: now.Add(invitationLifetime), CreatedBy: in.Principal.UserID}
	created, err := s.store.CreateInvitation(ctx, inv, digest)
	if err != nil {
		return Invitation{}, mapStoreError(err)
	}
	inv = created
	// The raw credential is used exactly once, after persistence, to compose the
	// message. It is never placed in Invitation, an audit record, or a log.
	inviteURL := s.inviteURL + "#token=" + raw
	if err := s.sendInvitation(ctx, inv.TenantID, email, inviteURL, inv.ExpiresAt, inv.ID); err != nil {
		// A failed compensation is safe: DELIVERY_PENDING is non-redeemable.
		// Still attempt durable revocation for lifecycle visibility and cleanup.
		if revokeErr := s.store.RevokeInvitation(ctx, inv.TenantID, inv.ID, in.Principal.UserID); revokeErr != nil {
			return Invitation{}, ErrStoreUnavailable
		}
		return Invitation{}, ErrStoreUnavailable
	}
	if err := s.store.MarkInvitationDelivered(ctx, inv.TenantID, inv.ID, in.Principal.UserID); err != nil {
		return Invitation{}, ErrStoreUnavailable
	}
	inv.Status = "PENDING"
	return inv, nil
}

func (s *Service) Resend(ctx context.Context, in ResendInput) error {
	_, err := s.ResendWithResult(ctx, in)
	return err
}

// ResendWithResult returns the replacement's redacted lifecycle projection so
// the caller can subsequently revoke or resend that new resource.
func (s *Service) ResendWithResult(ctx context.Context, in ResendInput) (Invitation, error) {
	if s == nil || !validMutationPrincipal(in.Principal) || !validUUID(in.InvitationID) {
		return Invitation{}, ErrInvitationInvalid
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: in.Principal, Password: in.Password, MFACode: in.MFACode}); err != nil {
		return Invitation{}, ErrStepUpFailed
	}
	old, found, err := s.store.FindInvitation(ctx, in.Principal.TenantID, in.InvitationID)
	if err != nil {
		return Invitation{}, mapStoreError(err)
	}
	if !found || old.Status != "PENDING" || !old.ExpiresAt.After(s.now()) {
		return Invitation{}, ErrInvitationInvalid
	}
	// Revoke first; a fresh invite is intentionally required so there can never
	// be two simultaneously usable invitation digests for one recipient.
	if err := s.store.RevokeInvitation(ctx, in.Principal.TenantID, in.InvitationID, in.Principal.UserID); err != nil {
		return Invitation{}, mapStoreError(err)
	}
	raw, digest, err := newInvitationToken()
	if err != nil {
		return Invitation{}, ErrStoreUnavailable
	}
	next, err := s.store.CreateInvitation(ctx, Invitation{TenantID: in.Principal.TenantID, Email: old.Email, Role: old.Role, Status: "DELIVERY_PENDING", ExpiresAt: s.now().UTC().Add(invitationLifetime), CreatedBy: in.Principal.UserID}, digest)
	if err != nil {
		return Invitation{}, mapStoreError(err)
	}
	if err := s.sendInvitation(ctx, next.TenantID, next.Email, s.inviteURL+"#token="+raw, next.ExpiresAt, next.ID); err != nil {
		if revokeErr := s.store.RevokeInvitation(ctx, next.TenantID, next.ID, in.Principal.UserID); revokeErr != nil {
			return Invitation{}, ErrStoreUnavailable
		}
		return Invitation{}, ErrStoreUnavailable
	}
	if err := s.store.MarkInvitationDelivered(ctx, next.TenantID, next.ID, in.Principal.UserID); err != nil {
		return Invitation{}, mapStoreError(err)
	}
	next.Status = "PENDING"
	return next, nil
}

func (s *Service) sendInvitation(ctx context.Context, tenantID, email, inviteURL string, expiresAt time.Time, invitationID string) error {
	if sender, ok := s.sender.(tenantInvitationIDSender); ok {
		return sender.SendStaffInvitationForTenantWithID(ctx, tenantID, email, inviteURL, expiresAt, invitationID)
	}
	if sender, ok := s.sender.(TenantStaffInvitationSender); ok {
		return sender.SendStaffInvitationForTenant(ctx, tenantID, email, inviteURL, expiresAt)
	}
	if sender, ok := s.sender.(invitationIDSender); ok {
		return sender.SendStaffInvitationWithID(ctx, email, inviteURL, expiresAt, invitationID)
	}
	return s.sender.SendStaffInvitation(ctx, email, inviteURL, expiresAt)
}

func (s *Service) Revoke(ctx context.Context, in RevokeInput) error {
	if s == nil || !validMutationPrincipal(in.Principal) || !validUUID(in.InvitationID) {
		return ErrInvitationInvalid
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: in.Principal, Password: in.Password, MFACode: in.MFACode}); err != nil {
		return ErrStepUpFailed
	}
	return mapStoreError(s.store.RevokeInvitation(ctx, in.Principal.TenantID, in.InvitationID, in.Principal.UserID))
}

func (s *Service) ChangeRole(ctx context.Context, in RoleChangeInput) error {
	if s == nil || !validMutationPrincipal(in.Principal) || !validUUID(in.UserID) || in.UserID == in.Principal.UserID || !validRole(in.Role) {
		return ErrInvitationInvalid
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: in.Principal, Password: in.Password, MFACode: in.MFACode}); err != nil {
		return ErrStepUpFailed
	}
	return mapStoreError(s.store.ChangeStaffRole(ctx, in.Principal.TenantID, in.Principal.UserID, in.UserID, in.Role))
}

func (s *Service) Deactivate(ctx context.Context, in DeactivateInput) error {
	if s == nil || !validMutationPrincipal(in.Principal) || !validUUID(in.UserID) || in.UserID == in.Principal.UserID {
		return ErrInvitationInvalid
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: in.Principal, Password: in.Password, MFACode: in.MFACode}); err != nil {
		return ErrStepUpFailed
	}
	return mapStoreError(s.store.DeactivateStaff(ctx, in.Principal.TenantID, in.Principal.UserID, in.UserID))
}

func (s *Service) PrepareAcceptance(ctx context.Context, raw string) (MFASetup, error) {
	inv, ok, err := s.validInvitation(ctx, raw)
	if err != nil || !ok {
		return MFASetup{}, ErrInvitationInvalid
	}
	if !presentEnvelope(inv.MFA) {
		secret, err := totp.GenerateSecret()
		if err != nil {
			return MFASetup{}, ErrStoreUnavailable
		}
		envelope, err := auth.SealTOTPSecret(ctx, s.wrapper, inv.TenantID, "staff-invitation", inv.ID, secret)
		if err != nil {
			return MFASetup{}, ErrStoreUnavailable
		}
		stored, err := s.store.CreateOrReuseInvitationMFA(ctx, inv, invitationDigestBytes(raw), envelope)
		if err != nil {
			return MFASetup{}, ErrStoreUnavailable
		}
		if stored.Ciphertext != nil && string(stored.Ciphertext) != string(envelope.Ciphertext) {
			secret, err = auth.OpenTOTPSecret(ctx, s.wrapper, inv.TenantID, "staff-invitation", inv.ID, stored)
			if err != nil {
				return MFASetup{}, ErrInvitationInvalid
			}
		}
		return mfaSetup(inv.Email, secret), nil
	}
	secret, err := auth.OpenTOTPSecret(ctx, s.wrapper, inv.TenantID, "staff-invitation", inv.ID, inv.MFA)
	if err != nil {
		return MFASetup{}, ErrInvitationInvalid
	}
	return mfaSetup(inv.Email, secret), nil
}

func (s *Service) CompleteAcceptance(ctx context.Context, in CompleteInvitationInput) error {
	if len(in.Password) < 16 || len(in.Password) > 1024 {
		return ErrInvitationInvalid
	}
	inv, ok, err := s.validInvitation(ctx, in.Token)
	if err != nil || !ok || !presentEnvelope(inv.MFA) {
		return ErrInvitationInvalid
	}
	secret, err := auth.OpenTOTPSecret(ctx, s.wrapper, inv.TenantID, "staff-invitation", inv.ID, inv.MFA)
	if err != nil {
		return ErrInvitationInvalid
	}
	if _, matched, err := totp.Verify(secret, strings.TrimSpace(in.MFACode), s.now(), totp.DefaultDigits, 1); err != nil || !matched {
		return ErrInvitationInvalid
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return ErrStoreUnavailable
	}
	if err := s.store.CompleteInvitation(ctx, inv, hash, inv.MFA); err != nil {
		return mapStoreError(err)
	}
	return nil
}

func (s *Service) validInvitation(ctx context.Context, raw string) (Invitation, bool, error) {
	digest, ok := invitationDigest(raw)
	if !ok {
		return Invitation{}, false, nil
	}
	inv, found, err := s.store.FindInvitationByDigest(ctx, digest)
	if err != nil || !found || inv.Status != "PENDING" || inv.ExpiresAt.IsZero() || !inv.ExpiresAt.After(s.now()) {
		return Invitation{}, false, err
	}
	return inv, true, nil
}

func newInvitationToken() (string, []byte, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(value[:])
	return raw, invitationDigestBytes(raw), nil
}
func invitationDigest(raw string) ([]byte, bool) {
	if len(raw) != 43 {
		return nil, false
	}
	value, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(value) != 32 {
		return nil, false
	}
	return invitationDigestBytes(raw), true
}
func invitationDigestBytes(raw string) []byte { sum := sha256.Sum256([]byte(raw)); return sum[:] }
func normalizeEmail(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(value)
	return value, err == nil && parsed.Address == value && len(value) <= 254
}
func validRole(role BuiltInRole) bool {
	for _, candidate := range BuiltInRoles() {
		if role == candidate {
			return true
		}
	}
	return false
}
func validMutationPrincipal(p auth.Principal) bool {
	return validUUID(p.TenantID) && validUUID(p.UserID) && p.HasPermission("team.write")
}
func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrLastAdministrator) {
		return ErrLastAdministrator
	}
	if errors.Is(err, ErrStaffConflict) {
		return ErrStaffConflict
	}
	if errors.Is(err, ErrInvitationInvalid) {
		return ErrInvitationInvalid
	}
	return ErrStoreUnavailable
}
func mfaSetup(email, secret string) MFASetup {
	return MFASetup{ManualKey: secret, URI: "otpauth://totp/NetCore:" + url.PathEscape(email) + "?secret=" + url.QueryEscape(secret) + "&issuer=NetCore&digits=6&period=30"}
}
func presentEnvelope(value auth.MFASecretEnvelope) bool {
	return len(value.Ciphertext) > 0 || len(value.Nonce) > 0 || len(value.WrappedDEK) > 0 || strings.TrimSpace(value.KEKKeyID) != ""
}

var _ passwordHasher = argon2id.Hasher{}
