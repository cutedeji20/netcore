package network

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netcore-isp/netcore/internal/auth"
)

type lifecycleStoreStub struct {
	created  AAAConfiguration
	loaded   AAAConfiguration
	saved    RadiusCredentialRecord
	status   NASStatus
	verified bool
	actor    MutationActor
}

func (s *lifecycleStoreStub) CreateRouter(_ context.Context, _ string, _ MutationActor, _ RouterCreateInput) (AAAConfiguration, error) {
	return s.created, nil
}
func (s *lifecycleStoreStub) LoadAAA(_ context.Context, _ string, _ string) (AAAConfiguration, error) {
	return s.loaded, nil
}
func (s *lifecycleStoreStub) SaveCredential(_ context.Context, actor MutationActor, record RadiusCredentialRecord) error {
	s.saved = record
	s.actor = actor
	return nil
}
func (s *lifecycleStoreStub) MarkVerified(_ context.Context, _ string, _ string, _ MutationActor) error {
	s.verified = true
	return nil
}
func (s *lifecycleStoreStub) SetNASStatus(_ context.Context, _ string, _ string, _ MutationActor, status NASStatus) error {
	s.status = status
	return nil
}

type stepUpStub struct{ err error }

func (s stepUpStub) VerifyStepUp(_ context.Context, _ auth.StepUpInput) error { return s.err }

func TestNetworkServiceExportEncryptsAndRendersOneTimePackage(t *testing.T) {
	store := &lifecycleStoreStub{loaded: AAAConfiguration{RouterID: "22222222-2222-4222-8222-222222222222", NASID: "33333333-3333-4333-8333-333333333333", NASIPAddress: "172.16.0.4", RadiusSourceIP: "172.16.1.9", ShortName: "RB5009-LK-01", Status: NASStatusDisabled, Version: 0}}
	service, err := NewService(store, credentialTestWrapper{keyID: "https://vault.example/keys/netcore-provider-kek/version"}, stepUpStub{}, "172.16.0.4")
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{TenantID: "11111111-1111-4111-8111-111111111111", UserID: "44444444-4444-4444-8444-444444444444"}

	setup, err := service.Export(context.Background(), principal, MutationActor{IP: "105.127.17.195", UserAgent: "NetCore-test"}, "password", "123456", store.loaded.RouterID)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if store.saved.Version != 1 || !store.saved.Valid() {
		t.Fatalf("saved record = %+v", store.saved)
	}
	if store.actor.UserID != principal.UserID || store.actor.IP != "105.127.17.195" || store.actor.UserAgent != "NetCore-test" {
		t.Fatalf("audit actor = %+v", store.actor)
	}
	if string(setup) == "" || !containsAll(string(setup), "client RB5009-LK-01", "ipaddr = 172.16.1.9", "address=172.16.0.4", "src-address=172.16.1.9", "secret=") {
		t.Fatalf("setup package missing required material: %s", setup)
	}
}

func TestNetworkServiceRejectsFailedStepUpBeforeExport(t *testing.T) {
	store := &lifecycleStoreStub{loaded: AAAConfiguration{RouterID: "22222222-2222-4222-8222-222222222222", NASID: "33333333-3333-4333-8333-333333333333", Version: 0}}
	service, err := NewService(store, credentialTestWrapper{keyID: "https://vault.example/keys/netcore-provider-kek/version"}, stepUpStub{err: errors.New("rejected")}, "172.16.0.4")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Export(context.Background(), auth.Principal{TenantID: "11111111-1111-4111-8111-111111111111", UserID: "44444444-4444-4444-8444-444444444444"}, MutationActor{}, "wrong", "000000", store.loaded.RouterID)
	if !errors.Is(err, ErrStepUpFailed) || store.saved.Version != 0 {
		t.Fatalf("Export error=%v saved=%+v", err, store.saved)
	}
}

func TestNetworkServiceChangesNASStatusOnlyAfterStepUp(t *testing.T) {
	store := &lifecycleStoreStub{}
	service, _ := NewService(store, credentialTestWrapper{keyID: "https://vault.example/keys/netcore-provider-kek/version"}, stepUpStub{}, "172.16.0.4")
	principal := auth.Principal{TenantID: "11111111-1111-4111-8111-111111111111", UserID: "44444444-4444-4444-8444-444444444444"}
	if err := service.SetStatus(context.Background(), principal, MutationActor{}, "current", "123456", "22222222-2222-4222-8222-222222222222", NASStatusActive); err != nil || store.status != NASStatusActive {
		t.Fatalf("SetStatus error=%v status=%q", err, store.status)
	}
}

func TestNetworkServiceRecordsPrivateRadiusTestBeforeActivation(t *testing.T) {
	store := &lifecycleStoreStub{}
	service, _ := NewService(store, credentialTestWrapper{keyID: "https://vault.example/keys/netcore-provider-kek/version"}, stepUpStub{}, "172.16.0.4")
	principal := auth.Principal{TenantID: "11111111-1111-4111-8111-111111111111", UserID: "44444444-4444-4444-8444-444444444444"}
	if err := service.VerifyAAA(context.Background(), principal, MutationActor{}, "current", "123456", "22222222-2222-4222-8222-222222222222", true); err != nil || !store.verified {
		t.Fatalf("VerifyAAA error=%v verified=%t", err, store.verified)
	}
	if err := service.VerifyAAA(context.Background(), principal, MutationActor{}, "current", "123456", "22222222-2222-4222-8222-222222222222", false); !errors.Is(err, ErrPrivateTestRequired) {
		t.Fatalf("VerifyAAA without confirmation error=%v", err)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
