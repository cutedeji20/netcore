package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/integrations"
)

// TenantPaystackCredentialResolver supplies one tenant's active dashboard
// credential immediately before a Paystack operation.
type TenantPaystackCredentialResolver interface {
	Resolve(context.Context, string, integrations.Provider) ([]byte, integrations.CredentialMetadata, error)
}

// TenantPaystackGateway keeps the public tenant selector separate from the
// credential itself. It does not cache a Paystack key between operations.
type TenantPaystackGateway struct {
	resolver TenantPaystackCredentialResolver
	tenantID string
	client   *http.Client
	baseURL  string
}

func NewTenantPaystackGateway(resolver TenantPaystackCredentialResolver, tenantID string, client *http.Client) (*TenantPaystackGateway, error) {
	if resolver == nil || strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("payments: tenant Paystack credential resolver and tenant are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if client.Timeout <= 0 {
		return nil, errors.New("payments: Paystack HTTP client requires a timeout")
	}
	return &TenantPaystackGateway{resolver: resolver, tenantID: strings.TrimSpace(tenantID), client: client, baseURL: paystackAPIBase}, nil
}

func (*TenantPaystackGateway) Name() string { return paystackName }

func (g *TenantPaystackGateway) Available() bool {
	return g != nil && g.resolver != nil && g.tenantID != "" && g.client != nil
}

// Check proves an active tenant credential can be loaded before Service
// reserves any pending payment state. The key bytes are cleared immediately.
func (g *TenantPaystackGateway) Check(ctx context.Context) error {
	if !g.Available() {
		return ErrGatewayUnavailable
	}
	secret, err := g.resolveSecret(ctx)
	if err != nil {
		return ErrGatewayUnavailable
	}
	clearPaystackCredential(secret)
	return nil
}

func (g *TenantPaystackGateway) Initialize(ctx context.Context, input GatewayInitialization) (GatewayCheckout, error) {
	if !g.Available() || !validPaystackReference(input.Reference) || input.AmountMinor <= 0 || !sameCurrency(input.Currency, input.Currency) || strings.TrimSpace(input.CustomerEmail) == "" || (strings.TrimSpace(input.CallbackURL) != "" && !validPaymentCallbackURL(input.CallbackURL)) {
		return GatewayCheckout{}, ErrGatewayUnavailable
	}
	secret, err := g.resolveSecret(ctx)
	if err != nil {
		return GatewayCheckout{}, err
	}
	defer clearPaystackCredential(secret)
	body, err := json.Marshal(struct {
		Email       string `json:"email"`
		Amount      string `json:"amount"`
		Currency    string `json:"currency"`
		Reference   string `json:"reference"`
		CallbackURL string `json:"callback_url,omitempty"`
	}{
		Email: strings.TrimSpace(input.CustomerEmail), Amount: strconv.FormatInt(input.AmountMinor, 10),
		Currency: strings.ToUpper(strings.TrimSpace(input.Currency)), Reference: input.Reference, CallbackURL: strings.TrimSpace(input.CallbackURL),
	})
	if err != nil {
		return GatewayCheckout{}, fmt.Errorf("payments: encode Paystack initialization: %w", err)
	}
	var response struct {
		Status bool `json:"status"`
		Data   struct {
			AuthorizationURL string `json:"authorization_url"`
			Reference        string `json:"reference"`
		} `json:"data"`
	}
	if err := g.doJSON(ctx, http.MethodPost, "/transaction/initialize", secret, body, &response); err != nil {
		return GatewayCheckout{}, err
	}
	if !response.Status || response.Data.Reference != input.Reference || !validCheckoutURL(response.Data.AuthorizationURL) {
		return GatewayCheckout{}, errors.New("payments: Paystack initialization rejected")
	}
	return GatewayCheckout{AuthorizationURL: response.Data.AuthorizationURL}, nil
}

func (g *TenantPaystackGateway) Verify(ctx context.Context, reference string) (GatewayVerification, error) {
	if !g.Available() || !validPaystackReference(reference) {
		return GatewayVerification{}, ErrGatewayUnavailable
	}
	secret, err := g.resolveSecret(ctx)
	if err != nil {
		return GatewayVerification{}, err
	}
	defer clearPaystackCredential(secret)
	var response struct {
		Status bool `json:"status"`
		Data   struct {
			Reference string      `json:"reference"`
			Status    string      `json:"status"`
			Amount    json.Number `json:"amount"`
			Currency  string      `json:"currency"`
			PaidAt    string      `json:"paid_at"`
		} `json:"data"`
	}
	if err := g.doJSON(ctx, http.MethodGet, "/transaction/verify/"+url.PathEscape(reference), secret, nil, &response); err != nil {
		return GatewayVerification{}, err
	}
	if !response.Status || response.Data.Reference != reference {
		return GatewayVerification{}, errors.New("payments: Paystack verification rejected")
	}
	amount, err := response.Data.Amount.Int64()
	if err != nil || amount <= 0 {
		return GatewayVerification{}, errors.New("payments: Paystack verification returned invalid amount")
	}
	verification := GatewayVerification{
		Reference: response.Data.Reference, Status: strings.ToUpper(strings.TrimSpace(response.Data.Status)),
		AmountMinor: amount, Currency: strings.ToUpper(strings.TrimSpace(response.Data.Currency)),
	}
	if verification.Status == StatusSuccess {
		verifiedAt, err := time.Parse(time.RFC3339, response.Data.PaidAt)
		if err != nil {
			return GatewayVerification{}, errors.New("payments: Paystack verification returned invalid paid_at")
		}
		verification.VerifiedAt = verifiedAt.UTC()
	}
	return verification, nil
}

func (g *TenantPaystackGateway) VerifyWebhookSignature(ctx context.Context, raw []byte, supplied string) error {
	if !g.Available() || len(raw) == 0 || len(raw) > paystackResponseMaxLen {
		return ErrWebhookInvalid
	}
	provided, err := hex.DecodeString(strings.TrimSpace(supplied))
	if err != nil || len(provided) != sha512.Size {
		return ErrWebhookInvalid
	}
	secret, err := g.resolveSecret(ctx)
	if err != nil {
		return fmt.Errorf("%w: resolve signing key", ErrGatewayUnavailable)
	}
	defer clearPaystackCredential(secret)
	mac := hmac.New(sha512.New, secret)
	_, _ = mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), provided) {
		return ErrWebhookInvalid
	}
	return nil
}

func (*TenantPaystackGateway) ParseWebhook(raw []byte) (GatewayWebhook, error) {
	return parsePaystackWebhook(raw)
}

func (g *TenantPaystackGateway) resolveSecret(ctx context.Context) ([]byte, error) {
	secret, metadata, err := g.resolver.Resolve(ctx, g.tenantID, integrations.ProviderPaystack)
	if err != nil || len(secret) == 0 || (metadata.PaystackMode != "TEST" && metadata.PaystackMode != "LIVE") {
		return nil, errors.New("payments: Paystack secret is unavailable")
	}
	return secret, nil
}

func (g *TenantPaystackGateway) doJSON(ctx context.Context, method, path string, secret, requestBody []byte, destination any) error {
	endpoint := strings.TrimRight(g.baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("payments: build Paystack request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+string(secret))
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("payments: Paystack request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, paystackResponseMaxLen+1))
	if err != nil || len(body) == 0 || len(body) > paystackResponseMaxLen {
		return errors.New("payments: invalid Paystack response")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("payments: Paystack returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("payments: invalid Paystack response")
	}
	return nil
}

func clearPaystackCredential(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
