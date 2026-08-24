// Package integrations manages the encrypted, tenant-scoped configuration
// required to call the fixed NetCore providers: Resend and Paystack.
package integrations

import (
	"errors"
	"strings"
)

var (
	ErrInvalidProvider   = errors.New("integrations: invalid provider")
	ErrInvalidTenant     = errors.New("integrations: invalid tenant")
	ErrInvalidCredential = errors.New("integrations: invalid credential")
	ErrInvalidEnvelope   = errors.New("integrations: invalid credential envelope")
	ErrKeyUnavailable    = errors.New("integrations: encryption key unavailable")
	ErrCredentialInvalid = errors.New("integrations: credential could not be decrypted")
)

// Provider is deliberately a closed list. Adding a provider requires an
// explicit data model, policy, API, and runtime integration review.
type Provider string

const (
	ProviderResend   Provider = "resend"
	ProviderPaystack Provider = "paystack"
)

func (p Provider) Valid() bool {
	return p == ProviderResend || p == ProviderPaystack
}

func validTenantID(tenantID string) bool {
	tenantID = strings.TrimSpace(tenantID)
	return tenantID != "" && !strings.ContainsAny(tenantID, "/\\\r\n\t")
}
