package integrations

import (
	"context"
	"errors"
	"strings"
)

// ActiveCredentialStore returns the complete envelope only for an active
// provider record in the requested tenant. It is deliberately narrower than
// Store so caller-facing read paths cannot accidentally select ciphertext.
type ActiveCredentialStore interface {
	LoadActive(context.Context, string, Provider) (Record, bool, error)
}

// CredentialMetadata contains the non-secret settings needed by a provider
// adapter after it has obtained an active credential.
type CredentialMetadata struct {
	SenderEmail  string
	PaystackMode string
}

// CredentialResolver decrypts a provider key on demand. It never caches
// plaintext, so a disabled/disconnected record takes effect on the next
// outbound provider operation.
type CredentialResolver struct {
	store   ActiveCredentialStore
	wrapper KeyWrapper
}

func NewCredentialResolver(store ActiveCredentialStore, wrapper KeyWrapper) (*CredentialResolver, error) {
	if store == nil {
		return nil, errors.New("integrations: active credential store is required")
	}
	if wrapper == nil {
		return nil, errors.New("integrations: key wrapper is required")
	}
	return &CredentialResolver{store: store, wrapper: wrapper}, nil
}

// Resolve returns an active tenant-scoped credential and its safe provider
// metadata. All unavailable, absent, disabled, malformed, or undecryptable
// records fail as the same neutral error.
func (r *CredentialResolver) Resolve(ctx context.Context, tenantID string, provider Provider) ([]byte, CredentialMetadata, error) {
	if r == nil || r.store == nil || r.wrapper == nil || !validTenantID(tenantID) || !provider.Valid() {
		return nil, CredentialMetadata{}, ErrCredentialInvalid
	}
	record, found, err := r.store.LoadActive(ctx, tenantID, provider)
	if err != nil || !found || record.TenantID != tenantID || record.Provider != provider || record.Status != StatusActive {
		return nil, CredentialMetadata{}, ErrCredentialInvalid
	}
	credential, err := DecryptCredential(ctx, r.wrapper, tenantID, provider, record.Envelope)
	if err != nil || len(credential) == 0 {
		return nil, CredentialMetadata{}, ErrCredentialInvalid
	}
	metadata := CredentialMetadata{SenderEmail: strings.TrimSpace(record.SenderEmail), PaystackMode: strings.ToUpper(strings.TrimSpace(record.PaystackMode))}
	if !validResolvedMetadata(provider, metadata) {
		for index := range credential {
			credential[index] = 0
		}
		return nil, CredentialMetadata{}, ErrCredentialInvalid
	}
	return credential, metadata, nil
}

func validResolvedMetadata(provider Provider, metadata CredentialMetadata) bool {
	switch provider {
	case ProviderResend:
		return metadata.SenderEmail != "" && metadata.PaystackMode == ""
	case ProviderPaystack:
		return metadata.SenderEmail == "" && (metadata.PaystackMode == "TEST" || metadata.PaystackMode == "LIVE")
	default:
		return false
	}
}
