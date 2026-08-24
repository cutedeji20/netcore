package integrations

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

const keyVaultWrapAlgorithm = "RSA-OAEP-256"

// azureKeyOperations is deliberately limited to the two Key Vault crypto
// operations required for envelope encryption. It cannot read Key Vault
// secrets or create/export a key.
type azureKeyOperations interface {
	WrapKey(context.Context, string, string, []byte) ([]byte, error)
	UnwrapKey(context.Context, string, string, []byte) ([]byte, error)
}

// AzureKeyVaultWrapper adapts the Key Vault Keys crypto surface to KeyWrapper.
// The concrete managed-identity client is injected at process startup.
type AzureKeyVaultWrapper struct {
	operations azureKeyOperations
	keyID      string
}

func NewAzureKeyVaultWrapper(operations azureKeyOperations, keyID string) (*AzureKeyVaultWrapper, error) {
	if operations == nil || !isAzureKeyVaultKeyID(keyID) {
		return nil, errors.New("integrations: Azure Key Vault key wrapper is invalid")
	}
	return &AzureKeyVaultWrapper{operations: operations, keyID: strings.TrimSpace(keyID)}, nil
}

// NewManagedIdentityKeyWrapper creates the production Key Vault wrapper. On
// Azure it authenticates with the VM's system-assigned managed identity; it
// neither accepts nor looks up an Azure client secret.
func NewManagedIdentityKeyWrapper(keyID string) (*AzureKeyVaultWrapper, error) {
	vaultURL, keyName, keyVersion, err := splitAzureKeyVaultKeyID(keyID)
	if err != nil {
		return nil, errors.New("integrations: Azure Key Vault key wrapper is invalid")
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, errors.New("integrations: Azure managed identity is unavailable")
	}
	client, err := azkeys.NewClient(vaultURL, credential, nil)
	if err != nil {
		return nil, errors.New("integrations: Azure Key Vault client is unavailable")
	}
	return NewAzureKeyVaultWrapper(&managedIdentityAzureKeyOperations{client: client, keyID: strings.TrimSpace(keyID), name: keyName, version: keyVersion}, keyID)
}

type managedIdentityAzureKeyOperations struct {
	client  *azkeys.Client
	keyID   string
	name    string
	version string
}

func (o *managedIdentityAzureKeyOperations) WrapKey(ctx context.Context, keyID, algorithm string, value []byte) ([]byte, error) {
	if o == nil || o.client == nil || keyID != o.keyID || algorithm != keyVaultWrapAlgorithm {
		return nil, ErrKeyUnavailable
	}
	keyAlgorithm := azkeys.EncryptionAlgorithmRSAOAEP256
	response, err := o.client.WrapKey(ctx, o.name, o.version, azkeys.KeyOperationParameters{Algorithm: &keyAlgorithm, Value: value}, nil)
	if err != nil || len(response.Result) == 0 {
		return nil, ErrKeyUnavailable
	}
	return append([]byte(nil), response.Result...), nil
}

func (o *managedIdentityAzureKeyOperations) UnwrapKey(ctx context.Context, keyID, algorithm string, value []byte) ([]byte, error) {
	if o == nil || o.client == nil || keyID != o.keyID || algorithm != keyVaultWrapAlgorithm {
		return nil, ErrKeyUnavailable
	}
	keyAlgorithm := azkeys.EncryptionAlgorithmRSAOAEP256
	response, err := o.client.UnwrapKey(ctx, o.name, o.version, azkeys.KeyOperationParameters{Algorithm: &keyAlgorithm, Value: value}, nil)
	if err != nil || len(response.Result) == 0 {
		return nil, ErrKeyUnavailable
	}
	return append([]byte(nil), response.Result...), nil
}

func (w *AzureKeyVaultWrapper) Wrap(ctx context.Context, dek []byte) (WrappedDEK, error) {
	if w == nil || w.operations == nil || len(dek) != dataEncryptionKeyLength {
		return WrappedDEK{}, ErrKeyUnavailable
	}
	wrapped, err := w.operations.WrapKey(ctx, w.keyID, keyVaultWrapAlgorithm, dek)
	if err != nil || len(wrapped) == 0 {
		return WrappedDEK{}, ErrKeyUnavailable
	}
	return WrappedDEK{Ciphertext: append([]byte(nil), wrapped...), KeyID: w.keyID}, nil
}

func (w *AzureKeyVaultWrapper) Unwrap(ctx context.Context, wrapped []byte, keyID string) ([]byte, error) {
	if w == nil || w.operations == nil || len(wrapped) == 0 || strings.TrimSpace(keyID) != w.keyID {
		return nil, ErrKeyUnavailable
	}
	dek, err := w.operations.UnwrapKey(ctx, w.keyID, keyVaultWrapAlgorithm, wrapped)
	if err != nil || len(dek) != dataEncryptionKeyLength {
		return nil, ErrKeyUnavailable
	}
	return append([]byte(nil), dek...), nil
}

func isAzureKeyVaultKeyID(value string) bool {
	_, _, _, err := splitAzureKeyVaultKeyID(value)
	return err == nil
}

func splitAzureKeyVaultKeyID(value string) (vaultURL, keyName, keyVersion string, err error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".vault.azure.net") {
		return "", "", "", errors.New("invalid key URL")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 3 || parts[0] != "keys" || parts[1] == "" || parts[2] == "" {
		return "", "", "", errors.New("invalid key path")
	}
	return parsed.Scheme + "://" + parsed.Host, parts[1], parts[2], nil
}
