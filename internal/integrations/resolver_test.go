package integrations

import (
	"context"
	"errors"
	"testing"
)

func TestCredentialResolverDecryptsOnlyAnActiveTenantProviderCredential(t *testing.T) {
	// This must fail if runtime provider delivery can load another tenant's
	// credential, or if disabled/disconnected settings remain usable.
	wrapper := testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}
	envelope, err := EncryptCredential(context.Background(), wrapper, "tenant-a", ProviderResend, []byte("re_runtime_credential"))
	if err != nil {
		t.Fatal(err)
	}
	store := &resolverStore{record: Record{
		TenantID: "tenant-a", Provider: ProviderResend, Status: StatusActive,
		Envelope: envelope, SenderEmail: "NetCore <access@example.test>",
	}}
	resolver, err := NewCredentialResolver(store, wrapper)
	if err != nil {
		t.Fatal(err)
	}

	credential, metadata, err := resolver.Resolve(context.Background(), "tenant-a", ProviderResend)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(credential) != "re_runtime_credential" || metadata.SenderEmail != "NetCore <access@example.test>" {
		t.Fatalf("resolved credential/metadata = %q %#v", credential, metadata)
	}
	if _, _, err := resolver.Resolve(context.Background(), "tenant-b", ProviderResend); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("other tenant resolve error = %v, want credential unavailable", err)
	}
	store.record.Status = StatusDisabled
	if _, _, err := resolver.Resolve(context.Background(), "tenant-a", ProviderResend); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("disabled credential error = %v, want credential unavailable", err)
	}
}

type resolverStore struct {
	record Record
}

func (s *resolverStore) LoadActive(_ context.Context, tenantID string, provider Provider) (Record, bool, error) {
	if s.record.TenantID != tenantID || s.record.Provider != provider {
		return Record{}, false, nil
	}
	return s.record, true, nil
}
