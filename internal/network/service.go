package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/integrations"
)

var (
	ErrStepUpFailed             = errors.New("network: step-up verification failed")
	ErrServiceUnavailable       = errors.New("network: service unavailable")
	ErrRadiusAddressUnavailable = errors.New("network: private RADIUS address is unavailable")
	ErrPrivateTestRequired      = errors.New("network: private RADIUS acceptance test confirmation is required")
)

type StepUpVerifier interface {
	VerifyStepUp(context.Context, auth.StepUpInput) error
}

// Service coordinates MFA-gated router lifecycle work. It does not own the
// RADIUS runtime and never persists a plaintext secret.
type Service struct {
	store         LifecycleStore
	wrapper       integrations.KeyWrapper
	stepUp        StepUpVerifier
	radiusAddress string
}

func NewService(store LifecycleStore, wrapper integrations.KeyWrapper, stepUp StepUpVerifier, radiusAddress string) (*Service, error) {
	if store == nil || wrapper == nil || stepUp == nil {
		return nil, ErrServiceUnavailable
	}
	radiusAddress = strings.TrimSpace(radiusAddress)
	if radiusAddress != "" && !privateRouterAddress(radiusAddress) {
		return nil, ErrRadiusAddressUnavailable
	}
	return &Service{store: store, wrapper: wrapper, stepUp: stepUp, radiusAddress: radiusAddress}, nil
}

// Export rotates the encrypted NAS secret and returns setup material once.
func (s *Service) Export(ctx context.Context, principal auth.Principal, actor MutationActor, password, mfaCode, routerID string) ([]byte, error) {
	if s == nil || s.store == nil || s.wrapper == nil || s.stepUp == nil || !validNetworkID(principal.TenantID) || !validNetworkID(principal.UserID) || !validNetworkID(routerID) {
		return nil, ErrServiceUnavailable
	}
	if s.radiusAddress == "" {
		return nil, ErrRadiusAddressUnavailable
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return nil, ErrStepUpFailed
	}
	configuration, err := s.store.LoadAAA(ctx, principal.TenantID, routerID)
	if err != nil {
		return nil, err
	}
	secret, err := GenerateSharedSecret()
	if err != nil {
		return nil, err
	}
	defer clear(secret)
	version := configuration.Version + 1
	envelope, err := EncryptNASSecret(ctx, s.wrapper, principal.TenantID, configuration.NASID, version, secret)
	if err != nil {
		return nil, err
	}
	actor.UserID = principal.UserID
	if err := s.store.SaveCredential(ctx, actor, RadiusCredentialRecord{TenantID: principal.TenantID, NASID: configuration.NASID, Version: version, Envelope: envelope}); err != nil {
		return nil, err
	}
	return renderSetupPackage(configuration, s.radiusAddress, secret), nil
}

// AAA returns non-secret NAS metadata for an authorised tenant.
func (s *Service) AAA(ctx context.Context, tenantID, routerID string) (AAAConfiguration, error) {
	if s == nil || s.store == nil || !validNetworkID(tenantID) || !validNetworkID(routerID) {
		return AAAConfiguration{}, ErrServiceUnavailable
	}
	return s.store.LoadAAA(ctx, tenantID, routerID)
}

// VerifyAAA records an administrator's fresh confirmation that the current
// secret passed Access-Request plus Start/Interim/Stop checks over the private
// path. Rotating the secret clears this attestation.
func (s *Service) VerifyAAA(ctx context.Context, principal auth.Principal, actor MutationActor, password, mfaCode, routerID string, confirmed bool) error {
	if s == nil || s.store == nil || s.stepUp == nil || !validNetworkID(principal.TenantID) || !validNetworkID(principal.UserID) || !validNetworkID(routerID) {
		return ErrServiceUnavailable
	}
	if !confirmed {
		return ErrPrivateTestRequired
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return ErrStepUpFailed
	}
	actor.UserID = principal.UserID
	return s.store.MarkVerified(ctx, principal.TenantID, routerID, actor)
}

// SetStatus performs the operator-confirmed activation or emergency disable.
func (s *Service) SetStatus(ctx context.Context, principal auth.Principal, actor MutationActor, password, mfaCode, routerID string, status NASStatus) error {
	if s == nil || s.store == nil || s.stepUp == nil || !validNetworkID(principal.TenantID) || !validNetworkID(principal.UserID) || !validNetworkID(routerID) || (status != NASStatusActive && status != NASStatusDisabled) {
		return ErrServiceUnavailable
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return ErrStepUpFailed
	}
	actor.UserID = principal.UserID
	return s.store.SetNASStatus(ctx, principal.TenantID, routerID, actor, status)
}

// CreateRouter creates a disabled NAS only after fresh staff confirmation.
func (s *Service) CreateRouter(ctx context.Context, principal auth.Principal, actor MutationActor, password, mfaCode string, input RouterCreateInput) (AAAConfiguration, error) {
	if s == nil || s.store == nil || s.stepUp == nil || !validNetworkID(principal.TenantID) || !validNetworkID(principal.UserID) || input.NormalizeAndValidate() != nil {
		return AAAConfiguration{}, ErrServiceUnavailable
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return AAAConfiguration{}, ErrStepUpFailed
	}
	actor.UserID = principal.UserID
	return s.store.CreateRouter(ctx, principal.TenantID, actor, input)
}

func renderSetupPackage(configuration AAAConfiguration, radiusAddress string, secret []byte) []byte {
	clientName := radiusClientName(configuration.ShortName, configuration.NASID)
	return []byte(fmt.Sprintf("# NetCore one-time RADIUS setup package\n# Apply only over the private management path. Do not retain this file.\n\n# FreeRADIUS clients.conf\nclient %s {\n  ipaddr = %s\n  secret = %s\n  shortname = %s\n  nastype = other\n}\n\n# RouterOS v7\n/radius add service=hotspot address=%s src-address=%s secret=%s authentication-port=1812 accounting-port=1813\n\n# Verify an Access-Request and Start/Interim/Stop accounting over the private VPN before marking this NAS active in NetCore.\n", clientName, configuration.RadiusSourceIP, secret, clientName, radiusAddress, configuration.RadiusSourceIP, secret))
}

func radiusClientName(shortName, nasID string) string {
	value := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, strings.TrimSpace(shortName))
	if value == "" || strings.Trim(value, "_") == "" {
		return "netcore_nas_" + strings.ReplaceAll(nasID, "-", "")
	}
	return value
}
