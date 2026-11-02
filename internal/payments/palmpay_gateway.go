package payments

import (
	"bytes"
	"context"
	"crypto"
	"crypto/md5" // PalmPay's published canonicalization protocol requires MD5 before RSA signing.
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // PalmPay's published protocol requires SHA1WithRSA.
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/integrations"
)

const (
	palmpayName           = "palmpay"
	palmpaySandboxAPIBase = "https://open-gw-sandbox.palmpay-inc.com"
	palmpayAPIBase        = "https://open-gw-prod.palmpay-inc.com"
	palmpayResponseMaxLen = 64 * 1024
	palmpayVersion        = "V1.1"
	palmpayChargeSuccess  = "payment.success"
	palmpayChargeFailed   = "payment.failed"
	palmpayChargeClosed   = "payment.closed"
)

// PalmPayCredential is serialized as one encrypted integration credential.
// The individual values are never persisted in plaintext or returned by the
// integration administration API.
type PalmPayCredential struct {
	Authorization      string `json:"authorization"`
	MerchantPrivateKey string `json:"merchant_private_key"`
	PalmPayPublicKey   string `json:"palmpay_public_key"`
	CountryCode        string `json:"country_code,omitempty"`
}

type TenantPalmPayCredentialResolver interface {
	Resolve(context.Context, string, integrations.Provider) ([]byte, integrations.CredentialMetadata, error)
}

type TenantPalmPayGateway struct {
	resolver TenantPalmPayCredentialResolver
	tenantID string
	client   *http.Client
	baseURL  string
	now      func() time.Time
}

func NewTenantPalmPayGateway(resolver TenantPalmPayCredentialResolver, tenantID string, client *http.Client) (*TenantPalmPayGateway, error) {
	if resolver == nil || strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("payments: tenant PalmPay credential resolver and tenant are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if client.Timeout <= 0 {
		return nil, errors.New("payments: PalmPay HTTP client requires a timeout")
	}
	return &TenantPalmPayGateway{resolver: resolver, tenantID: strings.TrimSpace(tenantID), client: client, baseURL: palmpayAPIBase, now: time.Now}, nil
}

func (*TenantPalmPayGateway) Name() string            { return palmpayName }
func (*TenantPalmPayGateway) SignatureHeader() string { return "" }

func (g *TenantPalmPayGateway) Available() bool {
	return g != nil && g.resolver != nil && g.tenantID != "" && g.client != nil && g.now != nil
}

func (g *TenantPalmPayGateway) Check(ctx context.Context) error {
	if !g.Available() {
		return ErrGatewayUnavailable
	}
	raw, _, credential, err := g.resolveCredential(ctx)
	clearPalmPayCredential(raw)
	clearPalmPayCredential([]byte(credential.MerchantPrivateKey))
	if err != nil {
		return ErrGatewayUnavailable
	}
	return nil
}

func (g *TenantPalmPayGateway) Initialize(ctx context.Context, input GatewayInitialization) (GatewayCheckout, error) {
	if !g.Available() || !validPalmPayReference(input.Reference) || input.AmountMinor <= 0 || !sameCurrency(input.Currency, "NGN") || strings.TrimSpace(input.CustomerEmail) == "" || !validPaymentCallbackURL(input.CallbackURL) {
		return GatewayCheckout{}, ErrGatewayUnavailable
	}
	rawCredential, metadata, credential, err := g.resolveCredential(ctx)
	if err != nil {
		return GatewayCheckout{}, err
	}
	defer clearPalmPayCredential(rawCredential)

	notifyURL, err := palmPayNotifyURL(input.CallbackURL)
	if err != nil {
		return GatewayCheckout{}, ErrGatewayUnavailable
	}
	payload := map[string]any{
		"requestTime": g.now().UTC().UnixMilli(),
		"version":     palmpayVersion,
		"nonceStr":    palmPayNonce(),
		"orderId":     input.Reference,
		"amount":      input.AmountMinor,
		"currency":    strings.ToUpper(strings.TrimSpace(input.Currency)),
		"notifyUrl":   notifyURL,
		"callBackUrl": strings.TrimSpace(input.CallbackURL),
		"title":       "NetCore voucher purchase",
		"description": "Prepaid internet access vouchers",
		"customerInfo": mustJSONString(map[string]string{
			"email": strings.TrimSpace(input.CustomerEmail),
		}),
		"goodsDetails": mustJSONString([]map[string]string{{"goodsId": input.Reference}}),
	}

	var response palmPayResponse[struct {
		OrderNo     string      `json:"orderNo"`
		OrderStatus int         `json:"orderStatus"`
		CheckoutURL string      `json:"checkoutUrl"`
		OrderAmount json.Number `json:"orderAmount"`
		Currency    string      `json:"currency"`
	}]
	if err := g.doJSON(ctx, metadata, credential, "/api/v2/payment/merchant/createorder", payload, &response); err != nil {
		return GatewayCheckout{}, err
	}
	amount, amountErr := response.Data.OrderAmount.Int64()
	if response.RespCode != "00000000" || strings.TrimSpace(response.Data.OrderNo) == "" || amountErr != nil || amount != input.AmountMinor || !sameCurrency(response.Data.Currency, input.Currency) || !validCheckoutURL(response.Data.CheckoutURL) {
		return GatewayCheckout{}, errors.New("payments: PalmPay initialization rejected")
	}
	return GatewayCheckout{AuthorizationURL: response.Data.CheckoutURL}, nil
}

func (g *TenantPalmPayGateway) Verify(ctx context.Context, reference string) (GatewayVerification, error) {
	if !g.Available() || !validPalmPayReference(reference) {
		return GatewayVerification{}, ErrGatewayUnavailable
	}
	rawCredential, metadata, credential, err := g.resolveCredential(ctx)
	if err != nil {
		return GatewayVerification{}, err
	}
	defer clearPalmPayCredential(rawCredential)
	payload := map[string]any{
		"requestTime": g.now().UTC().UnixMilli(),
		"version":     palmpayVersion,
		"nonceStr":    palmPayNonce(),
		"orderId":     reference,
	}
	var response palmPayResponse[palmPayOrder]
	if err := g.doJSON(ctx, metadata, credential, "/api/v2/payment/merchant/order/queryStatus", payload, &response); err != nil {
		return GatewayVerification{}, err
	}
	if response.RespCode != "00000000" || response.Data.OrderID != reference {
		return GatewayVerification{}, errors.New("payments: PalmPay verification rejected")
	}
	amount, err := response.Data.Amount.Int64()
	if err != nil || amount <= 0 {
		return GatewayVerification{}, errors.New("payments: PalmPay verification returned invalid amount")
	}
	verification := GatewayVerification{
		Reference: reference, Status: normalizePalmPayStatus(response.Data.OrderStatus),
		AmountMinor: amount, Currency: strings.ToUpper(strings.TrimSpace(response.Data.Currency)),
	}
	if verification.Status == StatusSuccess {
		if response.Data.CompletedTime <= 0 {
			return GatewayVerification{}, errors.New("payments: PalmPay verification returned invalid completion time")
		}
		verification.VerifiedAt = time.UnixMilli(response.Data.CompletedTime).UTC()
	}
	return verification, nil
}

func (g *TenantPalmPayGateway) VerifyWebhookSignature(ctx context.Context, raw []byte, _ string) error {
	if !g.Available() || len(raw) == 0 || len(raw) > palmpayResponseMaxLen {
		return ErrWebhookInvalid
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return ErrWebhookInvalid
	}
	supplied, ok := payload["sign"].(string)
	if !ok || strings.TrimSpace(supplied) == "" {
		return ErrWebhookInvalid
	}
	delete(payload, "sign")
	rawCredential, _, credential, err := g.resolveCredential(ctx)
	if err != nil {
		return fmt.Errorf("%w: resolve signing key", ErrGatewayUnavailable)
	}
	defer clearPalmPayCredential(rawCredential)
	publicKey, err := parsePalmPayPublicKey(credential.PalmPayPublicKey)
	if err != nil {
		return ErrWebhookInvalid
	}
	encodedSignature := strings.TrimSpace(supplied)
	signature, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil {
		// Some PalmPay integrations URL-encode the callback signature. Only
		// decode it as a fallback so literal '+' characters in normal Base64
		// are not changed into spaces.
		decodedSignature, unescapeErr := url.QueryUnescape(encodedSignature)
		if unescapeErr != nil {
			return ErrWebhookInvalid
		}
		signature, err = base64.StdEncoding.DecodeString(decodedSignature)
	}
	if err != nil {
		return ErrWebhookInvalid
	}
	canonical, err := palmPayCanonical(payload)
	if err != nil {
		return ErrWebhookInvalid
	}
	digest := palmPaySigningDigest(canonical)
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA1, digest, signature); err != nil {
		return ErrWebhookInvalid
	}
	return nil
}

func (*TenantPalmPayGateway) ParseWebhook(raw []byte) (GatewayWebhook, error) {
	var payload struct {
		OrderID     string `json:"orderId"`
		OrderNo     string `json:"orderNo"`
		OrderStatus int    `json:"orderStatus"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&payload); err != nil {
		return GatewayWebhook{}, ErrWebhookInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return GatewayWebhook{}, ErrWebhookInvalid
	}
	eventType := palmPayEventType(payload.OrderStatus)
	if !validPalmPayReference(payload.OrderID) || strings.TrimSpace(payload.OrderNo) == "" || eventType == "" {
		return GatewayWebhook{}, ErrWebhookInvalid
	}
	return GatewayWebhook{Provider: palmpayName, EventID: eventType + ":" + strings.TrimSpace(payload.OrderNo), EventType: eventType, Reference: strings.TrimSpace(payload.OrderID)}, nil
}

type palmPayOrder struct {
	OrderID       string      `json:"orderId"`
	OrderNo       string      `json:"orderNo"`
	Currency      string      `json:"currency"`
	Amount        json.Number `json:"amount"`
	OrderStatus   int         `json:"orderStatus"`
	CompletedTime int64       `json:"completedTime"`
}

type palmPayResponse[T any] struct {
	RespCode string `json:"respCode"`
	RespMsg  string `json:"respMsg"`
	Data     T      `json:"data"`
}

func (g *TenantPalmPayGateway) resolveCredential(ctx context.Context) ([]byte, integrations.CredentialMetadata, PalmPayCredential, error) {
	raw, metadata, err := g.resolver.Resolve(ctx, g.tenantID, integrations.ProviderPalmPay)
	if err != nil || len(raw) == 0 || (metadata.PalmPayMode != "TEST" && metadata.PalmPayMode != "LIVE") {
		clearPalmPayCredential(raw)
		return nil, integrations.CredentialMetadata{}, PalmPayCredential{}, errors.New("payments: PalmPay credential is unavailable")
	}
	var credential PalmPayCredential
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&credential); err != nil || strings.TrimSpace(credential.Authorization) == "" {
		clearPalmPayCredential(raw)
		return nil, integrations.CredentialMetadata{}, PalmPayCredential{}, errors.New("payments: PalmPay credential is unavailable")
	}
	if credential.CountryCode == "" {
		credential.CountryCode = "NG"
	}
	credential.CountryCode = strings.ToUpper(strings.TrimSpace(credential.CountryCode))
	if credential.CountryCode != "NG" {
		clearPalmPayCredential(raw)
		return nil, integrations.CredentialMetadata{}, PalmPayCredential{}, errors.New("payments: PalmPay country is unavailable")
	}
	if _, err := parsePalmPayPrivateKey(credential.MerchantPrivateKey); err != nil {
		clearPalmPayCredential(raw)
		return nil, integrations.CredentialMetadata{}, PalmPayCredential{}, errors.New("payments: PalmPay private key is invalid")
	}
	if _, err := parsePalmPayPublicKey(credential.PalmPayPublicKey); err != nil {
		clearPalmPayCredential(raw)
		return nil, integrations.CredentialMetadata{}, PalmPayCredential{}, errors.New("payments: PalmPay public key is invalid")
	}
	return raw, metadata, credential, nil
}

func (g *TenantPalmPayGateway) doJSON(ctx context.Context, metadata integrations.CredentialMetadata, credential PalmPayCredential, path string, payload map[string]any, destination any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("payments: encode PalmPay request")
	}
	privateKey, err := parsePalmPayPrivateKey(credential.MerchantPrivateKey)
	if err != nil {
		return errors.New("payments: PalmPay private key is invalid")
	}
	canonical, err := palmPayCanonical(payload)
	if err != nil {
		return errors.New("payments: encode PalmPay signature")
	}
	digest := palmPaySigningDigest(canonical)
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA1, digest)
	if err != nil {
		return errors.New("payments: sign PalmPay request")
	}
	baseURL := palmpayAPIBase
	if metadata.PalmPayMode == "TEST" {
		baseURL = palmpaySandboxAPIBase
	}
	if strings.TrimSpace(g.baseURL) != "" && g.baseURL != palmpayAPIBase {
		baseURL = g.baseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("payments: build PalmPay request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(credential.Authorization))
	req.Header.Set("CountryCode", credential.CountryCode)
	req.Header.Set("Signature", base64.StdEncoding.EncodeToString(signature))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	response, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("payments: PalmPay request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, palmpayResponseMaxLen+1))
	if err != nil || len(responseBody) == 0 || len(responseBody) > palmpayResponseMaxLen {
		return errors.New("payments: invalid PalmPay response")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("payments: PalmPay returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("payments: invalid PalmPay response")
	}
	return nil
}

func palmPayCanonical(payload map[string]any) (string, error) {
	keys := make([]string, 0, len(payload))
	for key, value := range payload {
		if value == nil || strings.TrimSpace(key) == "" {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value, err := palmPayStringValue(payload[key])
		if err != nil {
			return "", err
		}
		parts = append(parts, key+"="+strings.TrimSpace(value))
	}
	return strings.Join(parts, "&"), nil
}

func palmPayStringValue(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case bool:
		return strconv.FormatBool(typed), nil
	default:
		encoded, err := json.Marshal(typed)
		return string(encoded), err
	}
}

func palmPaySigningDigest(canonical string) []byte {
	md5Sum := md5.Sum([]byte(canonical))
	upperMD5 := strings.ToUpper(hex.EncodeToString(md5Sum[:]))
	sha1Sum := sha1.Sum([]byte(upperMD5))
	return sha1Sum[:]
}

func parsePalmPayPrivateKey(value string) (*rsa.PrivateKey, error) {
	der, err := palmPayKeyBytes(value)
	if err != nil {
		return nil, err
	}
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, rsaKey.Validate()
		}
	}
	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, err
	}
	return key, key.Validate()
}

func parsePalmPayPublicKey(value string) (*rsa.PublicKey, error) {
	der, err := palmPayKeyBytes(value)
	if err != nil {
		return nil, err
	}
	if parsed, err := x509.ParsePKIXPublicKey(der); err == nil {
		if key, ok := parsed.(*rsa.PublicKey); ok {
			return key, nil
		}
	}
	return x509.ParsePKCS1PublicKey(der)
}

func palmPayKeyBytes(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if block, _ := pem.Decode([]byte(trimmed)); block != nil {
		return block.Bytes, nil
	}
	return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(trimmed), ""))
}

func palmPayNotifyURL(callbackURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(callbackURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", ErrInvalidRequest
	}
	return parsed.Scheme + "://" + parsed.Host + "/webhooks/" + palmpayName, nil
}

func palmPayNonce() string {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UTC().UnixNano(), 16)
	}
	return hex.EncodeToString(raw[:])[:32]
}

func normalizePalmPayStatus(status int) string {
	switch status {
	case 0:
		return "UNPAID"
	case 1:
		return StatusPending
	case 2:
		return StatusSuccess
	case 3:
		return "FAILED"
	case 4:
		return "CLOSED"
	case 5:
		return "REQUIRED_CAPTURE"
	default:
		return "UNKNOWN"
	}
}

func palmPayEventType(status int) string {
	switch status {
	case 2:
		return palmpayChargeSuccess
	case 3:
		return palmpayChargeFailed
	case 4:
		return palmpayChargeClosed
	default:
		return ""
	}
}

func validPalmPayReference(reference string) bool {
	return validReference(reference) && len(strings.TrimSpace(reference)) <= 32
}

func mustJSONString(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func clearPalmPayCredential(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
