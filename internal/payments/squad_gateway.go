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

const (
	squadName           = "squad"
	squadAPIBase        = "https://api-d.squadco.com"
	squadSandboxAPIBase = "https://sandbox-api-d.squadco.com"
	squadResponseMaxLen = 64 * 1024
)

// TenantSquadCredentialResolver supplies a tenant's encrypted dashboard
// credential immediately before a Squad operation. The key is never cached.
type TenantSquadCredentialResolver interface {
	Resolve(context.Context, string, integrations.Provider) ([]byte, integrations.CredentialMetadata, error)
}

// TenantSquadGateway implements server-to-server initiation and verification
// against Squad's hosted checkout. It deliberately accepts only a reference
// and amount frozen by the payment service.
type TenantSquadGateway struct {
	resolver TenantSquadCredentialResolver
	tenantID string
	client   *http.Client
	baseURL  string
}

func NewTenantSquadGateway(resolver TenantSquadCredentialResolver, tenantID string, client *http.Client) (*TenantSquadGateway, error) {
	if resolver == nil || strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("payments: tenant Squad credential resolver and tenant are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if client.Timeout <= 0 {
		return nil, errors.New("payments: Squad HTTP client requires a timeout")
	}
	return &TenantSquadGateway{resolver: resolver, tenantID: strings.TrimSpace(tenantID), client: client, baseURL: squadAPIBase}, nil
}

func (*TenantSquadGateway) Name() string            { return squadName }
func (*TenantSquadGateway) SignatureHeader() string { return "X-Squad-Encrypted-Body" }

func (g *TenantSquadGateway) Available() bool {
	return g != nil && g.resolver != nil && g.tenantID != "" && g.client != nil
}

func (g *TenantSquadGateway) Check(ctx context.Context) error {
	if !g.Available() {
		return ErrGatewayUnavailable
	}
	secret, _, err := g.resolveSecret(ctx)
	if err != nil {
		return ErrGatewayUnavailable
	}
	clearSquadCredential(secret)
	return nil
}

func (g *TenantSquadGateway) Initialize(ctx context.Context, input GatewayInitialization) (GatewayCheckout, error) {
	if !g.Available() || !validReference(input.Reference) || input.AmountMinor <= 0 || !sameCurrency(input.Currency, input.Currency) || strings.TrimSpace(input.CustomerEmail) == "" || (strings.TrimSpace(input.CallbackURL) != "" && !validPaymentCallbackURL(input.CallbackURL)) {
		return GatewayCheckout{}, ErrGatewayUnavailable
	}
	secret, _, err := g.resolveSecret(ctx)
	if err != nil {
		return GatewayCheckout{}, err
	}
	defer clearSquadCredential(secret)
	payload := struct {
		Email        string            `json:"email"`
		Amount       string            `json:"amount"`
		Currency     string            `json:"currency"`
		InitiateType string            `json:"initiate_type"`
		Reference    string            `json:"transaction_ref"`
		CallbackURL  string            `json:"callback_url,omitempty"`
		Metadata     map[string]string `json:"metadata"`
	}{
		Email: strings.TrimSpace(input.CustomerEmail), Amount: strconv.FormatInt(input.AmountMinor, 10),
		Currency: strings.ToUpper(strings.TrimSpace(input.Currency)), InitiateType: "inline", Reference: input.Reference,
		CallbackURL: strings.TrimSpace(input.CallbackURL), Metadata: map[string]string{"netcore_payment_reference": input.Reference},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return GatewayCheckout{}, fmt.Errorf("payments: encode Squad initialization: %w", err)
	}
	var response struct {
		Status int `json:"status"`
		Data   struct {
			CheckoutURL string `json:"checkout_url"`
			Reference   string `json:"transaction_ref"`
		} `json:"data"`
	}
	if err := g.doJSON(ctx, http.MethodPost, "/transaction/initiate", secret, body, &response); err != nil {
		return GatewayCheckout{}, err
	}
	if response.Status != http.StatusOK || response.Data.Reference != input.Reference || !validCheckoutURL(response.Data.CheckoutURL) {
		return GatewayCheckout{}, errors.New("payments: Squad initialization rejected")
	}
	return GatewayCheckout{AuthorizationURL: response.Data.CheckoutURL}, nil
}

func (g *TenantSquadGateway) Verify(ctx context.Context, reference string) (GatewayVerification, error) {
	if !g.Available() || !validReference(reference) {
		return GatewayVerification{}, ErrGatewayUnavailable
	}
	secret, _, err := g.resolveSecret(ctx)
	if err != nil {
		return GatewayVerification{}, err
	}
	defer clearSquadCredential(secret)
	verification, err := g.verifyByReference(ctx, secret, reference)
	if err == nil {
		return verification, nil
	}

	// Keep the list query as a compatibility fallback for previously-created
	// checkout links while Squad rolls out the reference-specific endpoint.
	// The direct endpoint is authoritative because it does not depend on a
	// time window or pagination.
	return g.verifyByListing(ctx, secret, reference)
}

type squadTransaction struct {
	Reference string      `json:"transaction_ref"`
	Status    string      `json:"transaction_status"`
	Amount    json.Number `json:"transaction_amount"`
	Currency  string      `json:"transaction_currency_id"`
	CreatedAt string      `json:"created_at"`
	PaidAt    string      `json:"paid_at"`
}

func (g *TenantSquadGateway) verifyByReference(ctx context.Context, secret []byte, reference string) (GatewayVerification, error) {
	var response struct {
		Status  int              `json:"status"`
		Success bool             `json:"success"`
		Data    squadTransaction `json:"data"`
	}
	if err := g.doJSON(ctx, http.MethodGet, "/transaction/verify/"+url.PathEscape(reference), secret, nil, &response); err != nil {
		return GatewayVerification{}, err
	}
	if response.Status != http.StatusOK {
		return GatewayVerification{}, errors.New("payments: Squad verification rejected")
	}
	return squadVerification(response.Data, reference)
}

func (g *TenantSquadGateway) verifyByListing(ctx context.Context, secret []byte, reference string) (GatewayVerification, error) {
	day := time.Now().UTC().Format("2006-01-02")
	path := "/transaction?start_date=" + url.QueryEscape(day) + "&end_date=" + url.QueryEscape(day) + "&page=1&perpage=100&reference=" + url.QueryEscape(reference)
	var response struct {
		Status  int                `json:"status"`
		Success bool               `json:"success"`
		Data    []squadTransaction `json:"data"`
	}
	if err := g.doJSON(ctx, http.MethodGet, path, secret, nil, &response); err != nil {
		return GatewayVerification{}, err
	}
	if response.Status != http.StatusOK || !response.Success {
		return GatewayVerification{}, errors.New("payments: Squad verification rejected")
	}
	for _, item := range response.Data {
		if item.Reference != reference {
			continue
		}
		return squadVerification(item, reference)
	}
	return GatewayVerification{Reference: reference, Status: "PENDING"}, nil
}

func squadVerification(item squadTransaction, reference string) (GatewayVerification, error) {
	if item.Reference != reference {
		return GatewayVerification{}, errors.New("payments: Squad verification returned a different reference")
	}
	amount, err := item.Amount.Int64()
	if err != nil || amount <= 0 {
		return GatewayVerification{}, errors.New("payments: Squad verification returned invalid amount")
	}
	verification := GatewayVerification{Reference: reference, Status: normalizeSquadStatus(item.Status), AmountMinor: amount, Currency: strings.ToUpper(strings.TrimSpace(item.Currency))}
	if verification.Status != StatusSuccess {
		return verification, nil
	}
	verifiedAt, err := parseSquadTime(firstNonEmpty(item.PaidAt, item.CreatedAt))
	if err != nil {
		return GatewayVerification{}, errors.New("payments: Squad verification returned invalid payment time")
	}
	verification.VerifiedAt = verifiedAt
	return verification, nil
}

func normalizeSquadStatus(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "SUCCESS", "SUCCESSFUL", "PAID", "COMPLETED":
		return StatusSuccess
	default:
		return strings.ToUpper(strings.TrimSpace(value))
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (g *TenantSquadGateway) VerifyWebhookSignature(ctx context.Context, raw []byte, supplied string) error {
	if !g.Available() || len(raw) == 0 || len(raw) > squadResponseMaxLen {
		return ErrWebhookInvalid
	}
	provided, err := hex.DecodeString(strings.TrimSpace(supplied))
	if err != nil || len(provided) != sha512.Size {
		return ErrWebhookInvalid
	}
	secret, _, err := g.resolveSecret(ctx)
	if err != nil {
		return fmt.Errorf("%w: resolve signing key", ErrGatewayUnavailable)
	}
	defer clearSquadCredential(secret)
	mac := hmac.New(sha512.New, secret)
	_, _ = mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), provided) {
		return ErrWebhookInvalid
	}
	return nil
}

func (*TenantSquadGateway) ParseWebhook(raw []byte) (GatewayWebhook, error) {
	return parseSquadWebhook(raw)
}

func parseSquadWebhook(raw []byte) (GatewayWebhook, error) {
	var payload struct {
		Event          string `json:"Event"`
		TransactionRef string `json:"TransactionRef"`
		Body           struct {
			Reference  string `json:"transaction_ref"`
			GatewayRef string `json:"gateway_ref"`
		} `json:"Body"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&payload); err != nil {
		return GatewayWebhook{}, ErrWebhookInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return GatewayWebhook{}, ErrWebhookInvalid
	}
	reference := strings.TrimSpace(payload.TransactionRef)
	if reference == "" {
		reference = strings.TrimSpace(payload.Body.Reference)
	}
	eventID := strings.TrimSpace(payload.Event) + ":" + strings.TrimSpace(payload.Body.GatewayRef)
	if strings.HasSuffix(eventID, ":") {
		eventID = strings.TrimSpace(payload.Event) + ":" + reference
	}
	if !validWebhookProviderEvent(squadName, strings.TrimSpace(payload.Event)) || !validReference(reference) || eventID == ":" {
		return GatewayWebhook{}, ErrWebhookInvalid
	}
	return GatewayWebhook{Provider: squadName, EventID: eventID, EventType: strings.TrimSpace(payload.Event), Reference: reference}, nil
}

func (g *TenantSquadGateway) resolveSecret(ctx context.Context) ([]byte, integrations.CredentialMetadata, error) {
	secret, metadata, err := g.resolver.Resolve(ctx, g.tenantID, integrations.ProviderSquad)
	if err != nil || len(secret) == 0 || (metadata.SquadMode != "TEST" && metadata.SquadMode != "LIVE") {
		return nil, integrations.CredentialMetadata{}, errors.New("payments: Squad secret is unavailable")
	}
	return secret, metadata, nil
}

func (g *TenantSquadGateway) doJSON(ctx context.Context, method, path string, secret, requestBody []byte, destination any) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(g.baseURL, "/")+path, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("payments: build Squad request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+string(secret))
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("payments: Squad request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, squadResponseMaxLen+1))
	if err != nil || len(body) == 0 || len(body) > squadResponseMaxLen {
		return errors.New("payments: invalid Squad response")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("payments: Squad returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("payments: invalid Squad response")
	}
	return nil
}

func parseSquadTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z07:00", "2006-01-02T15:04:05.000"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("invalid Squad timestamp")
}

func clearSquadCredential(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
