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
)

const (
	paystackName           = "paystack"
	paystackAPIBase        = "https://api.paystack.co"
	paystackResponseMaxLen = 64 * 1024
)

// SecretResolver is fulfilled by the deployment SecretStore. The logical ref
// is configured separately from its value, so a payment secret can never be
// read from the browser, database, or regular process configuration.
type SecretResolver interface {
	Resolve(context.Context, string) (string, error)
}

// WebhookGateway is the provider-specific half of the webhook boundary. It
// verifies the signature over raw bytes and extracts only the event identity
// needed for asynchronous server-to-server verification.
type WebhookGateway interface {
	Name() string
	VerifyWebhookSignature(context.Context, []byte, string) error
	ParseWebhook([]byte) (GatewayWebhook, error)
}

// GatewayWebhook is intentionally small. Its fields are signed but still not
// trusted as payment facts; the worker always calls Gateway.Verify afterwards.
type GatewayWebhook struct {
	Provider  string
	EventID   string
	EventType string
	Reference string
}

// PaystackGateway implements checkout initialization, independent transaction
// verification, and raw-body webhook signatures using the same secret-store
// reference. It does not keep the secret in its struct or logs.
type PaystackGateway struct {
	secrets   SecretResolver
	secretRef string
	client    *http.Client
	baseURL   string
}

func NewPaystackGateway(secrets SecretResolver, secretRef string, client *http.Client) (*PaystackGateway, error) {
	if secrets == nil || strings.TrimSpace(secretRef) == "" {
		return nil, errors.New("payments: Paystack secret resolver and reference are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if client.Timeout <= 0 {
		return nil, errors.New("payments: Paystack HTTP client requires a timeout")
	}
	return &PaystackGateway{
		secrets: secrets, secretRef: strings.TrimSpace(secretRef), client: client, baseURL: paystackAPIBase,
	}, nil
}

func (*PaystackGateway) Name() string { return paystackName }
func (g *PaystackGateway) Available() bool {
	return g != nil && g.secrets != nil && g.secretRef != "" && g.client != nil
}

func (g *PaystackGateway) Initialize(ctx context.Context, input GatewayInitialization) (GatewayCheckout, error) {
	if !g.Available() || !validPaystackReference(input.Reference) || input.AmountMinor <= 0 || !sameCurrency(input.Currency, input.Currency) || strings.TrimSpace(input.CustomerEmail) == "" {
		return GatewayCheckout{}, ErrGatewayUnavailable
	}
	secret, err := g.resolveSecret(ctx)
	if err != nil {
		return GatewayCheckout{}, err
	}
	body, err := json.Marshal(struct {
		Email     string `json:"email"`
		Amount    string `json:"amount"`
		Currency  string `json:"currency"`
		Reference string `json:"reference"`
	}{
		Email: strings.TrimSpace(input.CustomerEmail), Amount: strconv.FormatInt(input.AmountMinor, 10),
		Currency: strings.ToUpper(strings.TrimSpace(input.Currency)), Reference: input.Reference,
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

func (g *PaystackGateway) Verify(ctx context.Context, reference string) (GatewayVerification, error) {
	if !g.Available() || !validPaystackReference(reference) {
		return GatewayVerification{}, ErrGatewayUnavailable
	}
	secret, err := g.resolveSecret(ctx)
	if err != nil {
		return GatewayVerification{}, err
	}
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

// VerifyWebhookSignature authenticates exactly the bytes received over HTTP.
// It decodes the supplied hex signature before hmac.Equal, avoiding a
// timing-sensitive string comparison and accepting no malformed value.
func (g *PaystackGateway) VerifyWebhookSignature(ctx context.Context, raw []byte, supplied string) error {
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
	mac := hmac.New(sha512.New, []byte(secret))
	_, _ = mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), provided) {
		return ErrWebhookInvalid
	}
	return nil
}

func (*PaystackGateway) ParseWebhook(raw []byte) (GatewayWebhook, error) {
	var payload struct {
		Event string `json:"event"`
		Data  struct {
			ID        json.Number `json:"id"`
			Reference string      `json:"reference"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return GatewayWebhook{}, ErrWebhookInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return GatewayWebhook{}, ErrWebhookInvalid
	}
	id := payload.Data.ID.String()
	if !validPaystackEventType(payload.Event) || !validPositiveDecimal(id) || !validReference(payload.Data.Reference) {
		return GatewayWebhook{}, ErrWebhookInvalid
	}
	return GatewayWebhook{
		Provider: paystackName, EventID: payload.Event + ":" + id,
		EventType: payload.Event, Reference: payload.Data.Reference,
	}, nil
}

func (g *PaystackGateway) resolveSecret(ctx context.Context) (string, error) {
	secret, err := g.secrets.Resolve(ctx, g.secretRef)
	if err != nil || strings.TrimSpace(secret) == "" {
		return "", errors.New("payments: Paystack secret is unavailable")
	}
	return secret, nil
}

func (g *PaystackGateway) doJSON(ctx context.Context, method, path, secret string, requestBody []byte, destination any) error {
	endpoint := strings.TrimRight(g.baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("payments: build Paystack request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
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

func validPaystackReference(reference string) bool {
	return len(reference) == 36 && strings.HasPrefix(reference, "pay-") && validReference(reference)
}

func validPaystackEventType(value string) bool {
	if len(value) < 3 || len(value) > 80 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validPositiveDecimal(value string) bool {
	if len(value) == 0 || len(value) > 20 || value == "0" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
