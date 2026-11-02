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

type activePaymentGatewayStore interface {
	ActivePaymentGateway(context.Context, string) (Provider, bool, error)
}

// CredentialMetadata contains the non-secret settings needed by a provider
// adapter after it has obtained an active credential.
type CredentialMetadata struct {
	SenderEmail  string
	PaystackMode string
	SquadMode    string
	PalmPayMode  string
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
	metadata := CredentialMetadata{SenderEmail: strings.TrimSpace(record.SenderEmail), PaystackMode: strings.ToUpper(strings.TrimSpace(record.PaystackMode)), SquadMode: strings.ToUpper(strings.TrimSpace(record.SquadMode)), PalmPayMode: strings.ToUpper(strings.TrimSpace(record.PalmPayMode))}
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
	case ProviderSquad:
		return metadata.SenderEmail == "" && (metadata.SquadMode == "TEST" || metadata.SquadMode == "LIVE")
	case ProviderPalmPay:
		return metadata.SenderEmail == "" && (metadata.PalmPayMode == "TEST" || metadata.PalmPayMode == "LIVE")
	default:
		return false
	}
}

// ActivePaymentGateway exposes only the safe provider selector. It does not
// resolve a credential and therefore cannot leak payment secrets.
func (r *CredentialResolver) ActivePaymentGateway(ctx context.Context, tenantID string) (string, bool, error) {
	if r == nil || !validTenantID(tenantID) {
		return "", false, ErrCredentialInvalid
	}
	store, ok := r.store.(activePaymentGatewayStore)
	if !ok {
		return "", false, ErrStoreUnavailable
	}
	provider, found, err := store.ActivePaymentGateway(ctx, tenantID)
	if err != nil || (found && !paymentProvider(provider)) {
		return "", false, ErrStoreUnavailable
	}
	return string(provider), found, nil
}
