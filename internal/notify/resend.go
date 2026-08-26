// Package notify contains production notification adapters. Providers receive
// only the destination and message required for delivery; application secrets
// stay behind logical SecretStore references.
package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/integrations"
	"github.com/netcore-isp/netcore/internal/payments"
	"github.com/netcore-isp/netcore/pkg/money"
)

const resendAPIBaseURL = "https://api.resend.com"

// SecretResolver is satisfied by the deployment secret store. The resolved
// value is used only for the outbound provider request and is never retained.
type SecretResolver interface {
	Resolve(context.Context, string) (string, error)
}

// ResendNotifier sends customer authentication messages through Resend's
// HTTPS API. It deliberately holds only a logical key reference, not the key.
type ResendNotifier struct {
	secrets   SecretResolver
	secretRef string
	from      string
	client    *http.Client
	baseURL   string
}

// TenantResendCredentialResolver supplies a short-lived Resend credential
// decrypted from the tenant's active dashboard configuration.
type TenantResendCredentialResolver interface {
	Resolve(context.Context, string, integrations.Provider) ([]byte, integrations.CredentialMetadata, error)
}

// TenantResendNotifier obtains the tenant's credential immediately before
// delivery. It intentionally does not keep a provider key in memory between
// requests, so disabled/disconnected settings take effect without a restart.
type TenantResendNotifier struct {
	resolver TenantResendCredentialResolver
	tenantID string
	client   *http.Client
	baseURL  string
}

// StaffInvitationSender is implemented by tenant-scoped notifiers. The URL is
// intentionally accepted only as HTTPS with its credential in a fragment;
// fragments do not travel in HTTP requests or Referer headers.
type StaffInvitationSender interface {
	SendStaffInvitation(context.Context, string, string, time.Time) error
}

// TenantInvitationSender selects the active credential for the invitation's
// tenant immediately before delivery. It is safe for use by a shared API
// service handling invitations for more than one tenant.
type TenantInvitationSender struct {
	resolver TenantResendCredentialResolver
	client   *http.Client
}

func NewTenantInvitationSender(resolver TenantResendCredentialResolver, client *http.Client) (*TenantInvitationSender, error) {
	if resolver == nil {
		return nil, errors.New("notify: tenant Resend credential resolver is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if client.Timeout <= 0 {
		return nil, errors.New("notify: Resend HTTP client requires a timeout")
	}
	return &TenantInvitationSender{resolver: resolver, client: client}, nil
}
func (s *TenantInvitationSender) SendStaffInvitation(context.Context, string, string, time.Time) error {
	return errors.New("notify: tenant is required for staff invitation")
}
func (s *TenantInvitationSender) SendStaffInvitationForTenant(ctx context.Context, tenantID, to, inviteURL string, expiresAt time.Time) error {
	notifier, err := NewTenantResendNotifier(s.resolver, tenantID, s.client)
	if err != nil {
		return err
	}
	return notifier.SendStaffInvitation(ctx, to, inviteURL, expiresAt)
}
func (s *TenantInvitationSender) SendStaffInvitationForTenantWithID(ctx context.Context, tenantID, to, inviteURL string, expiresAt time.Time, invitationID string) error {
	notifier, err := NewTenantResendNotifier(s.resolver, tenantID, s.client)
	if err != nil {
		return err
	}
	return notifier.SendStaffInvitationWithID(ctx, to, inviteURL, expiresAt, invitationID)
}

func NewTenantResendNotifier(resolver TenantResendCredentialResolver, tenantID string, client *http.Client) (*TenantResendNotifier, error) {
	if resolver == nil || strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("notify: tenant Resend credential resolver and tenant are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if client.Timeout <= 0 {
		return nil, errors.New("notify: Resend HTTP client requires a timeout")
	}
	return &TenantResendNotifier{resolver: resolver, tenantID: strings.TrimSpace(tenantID), client: client, baseURL: resendAPIBaseURL}, nil
}

func NewResendNotifier(secrets SecretResolver, secretRef, from string, client *http.Client) (*ResendNotifier, error) {
	if secrets == nil || strings.TrimSpace(secretRef) == "" {
		return nil, errors.New("notify: Resend secret resolver and reference are required")
	}
	if _, err := mail.ParseAddress(strings.TrimSpace(from)); err != nil {
		return nil, errors.New("notify: Resend sender is invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if client.Timeout <= 0 {
		return nil, errors.New("notify: Resend HTTP client requires a timeout")
	}
	return &ResendNotifier{
		secrets: secrets, secretRef: strings.TrimSpace(secretRef), from: strings.TrimSpace(from), client: client, baseURL: resendAPIBaseURL,
	}, nil
}

// SendOTP implements auth.OTPNotifier. It has no logging path because OTP
// codes and destinations are authentication data, not operational telemetry.
func (n *ResendNotifier) SendOTP(ctx context.Context, purpose auth.OTPPurpose, destination, code string, _ time.Time) error {
	if n == nil || n.secrets == nil || n.client == nil || strings.TrimSpace(n.baseURL) == "" {
		return errors.New("notify: Resend notifier is unavailable")
	}
	destination = strings.TrimSpace(destination)
	parsed, err := mail.ParseAddress(destination)
	if err != nil || parsed.Address != destination {
		return errors.New("notify: email destination is invalid")
	}
	subject, text, ok := otpMessage(purpose, code)
	if !ok {
		return errors.New("notify: unsupported OTP purpose")
	}
	key, err := n.secrets.Resolve(ctx, n.secretRef)
	if err != nil || strings.TrimSpace(key) == "" {
		return errors.New("notify: Resend credential is unavailable")
	}
	payload, err := json.Marshal(struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}{From: n.from, To: destination, Subject: subject, Text: text})
	if err != nil {
		return fmt.Errorf("notify: encode Resend message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL+"/emails", bytes.NewReader(payload))
	if err != nil {
		return errors.New("notify: Resend request is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "netcore-notifier/1.0")
	response, err := n.client.Do(req)
	if err != nil {
		return errors.New("notify: Resend delivery is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("notify: Resend delivery was rejected")
	}
	return nil
}

// SendPaymentReceipt sends only the receipt facts frozen after the provider
// verification completed. The event-scoped idempotency key lets Resend reject
// a duplicate request if a worker retries after an ambiguous network result.
func (n *ResendNotifier) SendPaymentReceipt(ctx context.Context, receipt payments.ReceiptEmail) error {
	if n == nil || n.secrets == nil || n.client == nil || strings.TrimSpace(n.baseURL) == "" {
		return errors.New("notify: Resend notifier is unavailable")
	}
	destination := strings.TrimSpace(receipt.To)
	parsed, err := mail.ParseAddress(destination)
	if err != nil || parsed.Address != destination || !validReceiptFacts(receipt) {
		return errors.New("notify: payment receipt is invalid")
	}
	amount, err := money.New(receipt.AmountMinor, receipt.Currency)
	if err != nil {
		return errors.New("notify: payment receipt amount is invalid")
	}
	key, err := n.secrets.Resolve(ctx, n.secretRef)
	if err != nil || strings.TrimSpace(key) == "" {
		return errors.New("notify: Resend credential is unavailable")
	}
	payload, err := json.Marshal(struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}{
		From:    n.from,
		To:      destination,
		Subject: "Your NetCore payment receipt",
		Text: "Your NetCore payment has been verified.\n\n" +
			"Plan: " + strings.TrimSpace(receipt.PlanName) + "\n" +
			"Amount: " + amount.String() + "\n" +
			"Paystack reference: " + strings.TrimSpace(receipt.Reference) + "\n" +
			"Access starts: " + receipt.StartsAt.UTC().Format(time.RFC3339) + "\n" +
			"Access expires: " + receipt.ExpiresAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("notify: encode Resend receipt: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL+"/emails", bytes.NewReader(payload))
	if err != nil {
		return errors.New("notify: Resend request is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "payment.receipt.requested/"+receipt.EventID)
	req.Header.Set("User-Agent", "netcore-notifier/1.0")
	response, err := n.client.Do(req)
	if err != nil {
		return errors.New("notify: Resend delivery is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("notify: Resend delivery was rejected")
	}
	return nil
}

// SendOTP implements auth.OTPNotifier for a fixed public portal tenant. OTP
// destinations and codes remain outside operational logs.
func (n *TenantResendNotifier) SendOTP(ctx context.Context, purpose auth.OTPPurpose, destination, code string, _ time.Time) error {
	if n == nil || n.resolver == nil || n.client == nil || strings.TrimSpace(n.baseURL) == "" {
		return errors.New("notify: tenant Resend notifier is unavailable")
	}
	destination = strings.TrimSpace(destination)
	parsed, err := mail.ParseAddress(destination)
	if err != nil || parsed.Address != destination {
		return errors.New("notify: email destination is invalid")
	}
	subject, text, ok := otpMessage(purpose, code)
	if !ok {
		return errors.New("notify: unsupported OTP purpose")
	}
	key, metadata, err := n.resolver.Resolve(ctx, n.tenantID, integrations.ProviderResend)
	if err != nil || len(key) == 0 || !validResendSender(metadata.SenderEmail) {
		return errors.New("notify: Resend credential is unavailable")
	}
	defer clearCredential(key)
	payload, err := json.Marshal(struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}{From: strings.TrimSpace(metadata.SenderEmail), To: destination, Subject: subject, Text: text})
	if err != nil {
		return fmt.Errorf("notify: encode Resend message: %w", err)
	}
	return n.send(ctx, key, payload, "")
}

// SendStaffInvitation resolves the active tenant credential at delivery time.
// It never logs the invite URL, provider credential, or provider response.
func (n *TenantResendNotifier) SendStaffInvitation(ctx context.Context, to, inviteURL string, expiresAt time.Time) error {
	return n.sendStaffInvitation(ctx, to, inviteURL, expiresAt, "")
}

// SendStaffInvitationWithID uses the non-secret invitation UUID as the
// provider idempotency key. The credential remains only in the link fragment.
func (n *TenantResendNotifier) SendStaffInvitationWithID(ctx context.Context, to, inviteURL string, expiresAt time.Time, invitationID string) error {
	if !validInvitationUUID(invitationID) {
		return errors.New("notify: staff invitation is invalid")
	}
	return n.sendStaffInvitation(ctx, to, inviteURL, expiresAt, invitationID)
}

func validInvitationUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if !((value[i] >= '0' && value[i] <= '9') || (value[i] >= 'a' && value[i] <= 'f') || (value[i] >= 'A' && value[i] <= 'F')) {
			return false
		}
	}
	return true
}

func (n *TenantResendNotifier) sendStaffInvitation(ctx context.Context, to, inviteURL string, expiresAt time.Time, invitationID string) error {
	if n == nil || n.resolver == nil || n.client == nil || strings.TrimSpace(n.baseURL) == "" {
		return errors.New("notify: tenant Resend notifier is unavailable")
	}
	destination := strings.TrimSpace(to)
	parsed, err := mail.ParseAddress(destination)
	if err != nil || parsed.Address != destination || expiresAt.IsZero() || !expiresAt.After(time.Now()) {
		return errors.New("notify: staff invitation is invalid")
	}
	link, err := url.Parse(strings.TrimSpace(inviteURL))
	if err != nil || link.Scheme != "https" || link.Host == "" || link.RawQuery != "" || !strings.HasPrefix(link.Fragment, "token=") || len(strings.TrimPrefix(link.Fragment, "token=")) != 43 {
		return errors.New("notify: staff invitation is invalid")
	}
	key, metadata, err := n.resolver.Resolve(ctx, n.tenantID, integrations.ProviderResend)
	if err != nil || len(key) == 0 || !validResendSender(metadata.SenderEmail) {
		return errors.New("notify: Resend credential is unavailable")
	}
	defer clearCredential(key)
	payload, err := json.Marshal(struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}{
		From: strings.TrimSpace(metadata.SenderEmail), To: destination, Subject: "You're invited to NetCore", Text: "Use this secure invitation link to set your password and authenticator app before " + expiresAt.UTC().Format(time.RFC3339) + ".\n\n" + link.String(),
	})
	if err != nil {
		return errors.New("notify: encode staff invitation")
	}
	if invitationID != "" {
		return n.send(ctx, key, payload, "staff.invitation/"+invitationID)
	}
	// The fallback key carries only a one-way URL digest, never the credential
	// or recipient; the staff service always provides an invitation UUID.
	digest := sha256.Sum256([]byte(link.String()))
	return n.send(ctx, key, payload, "staff.invitation/"+hex.EncodeToString(digest[:]))
}

// SendPaymentReceipt implements payments.ReceiptSender for a fixed tenant.
// Receipt data was already frozen after payment verification by the worker.
func (n *TenantResendNotifier) SendPaymentReceipt(ctx context.Context, receipt payments.ReceiptEmail) error {
	if n == nil || n.resolver == nil || n.client == nil || strings.TrimSpace(n.baseURL) == "" {
		return errors.New("notify: tenant Resend notifier is unavailable")
	}
	destination := strings.TrimSpace(receipt.To)
	parsed, err := mail.ParseAddress(destination)
	if err != nil || parsed.Address != destination || !validReceiptFacts(receipt) {
		return errors.New("notify: payment receipt is invalid")
	}
	amount, err := money.New(receipt.AmountMinor, receipt.Currency)
	if err != nil {
		return errors.New("notify: payment receipt amount is invalid")
	}
	key, metadata, err := n.resolver.Resolve(ctx, n.tenantID, integrations.ProviderResend)
	if err != nil || len(key) == 0 || !validResendSender(metadata.SenderEmail) {
		return errors.New("notify: Resend credential is unavailable")
	}
	defer clearCredential(key)
	payload, err := json.Marshal(struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}{
		From:    strings.TrimSpace(metadata.SenderEmail),
		To:      destination,
		Subject: "Your NetCore payment receipt",
		Text: "Your NetCore payment has been verified.\n\n" +
			"Plan: " + strings.TrimSpace(receipt.PlanName) + "\n" +
			"Amount: " + amount.String() + "\n" +
			"Paystack reference: " + strings.TrimSpace(receipt.Reference) + "\n" +
			"Access starts: " + receipt.StartsAt.UTC().Format(time.RFC3339) + "\n" +
			"Access expires: " + receipt.ExpiresAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("notify: encode Resend receipt: %w", err)
	}
	return n.send(ctx, key, payload, "payment.receipt.requested/"+receipt.EventID)
}

func (n *TenantResendNotifier) send(ctx context.Context, key, payload []byte, idempotencyKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL+"/emails", bytes.NewReader(payload))
	if err != nil {
		return errors.New("notify: Resend request is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+string(key))
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	req.Header.Set("User-Agent", "netcore-notifier/1.0")
	response, err := n.client.Do(req)
	if err != nil {
		return errors.New("notify: Resend delivery is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("notify: Resend delivery was rejected")
	}
	return nil
}

func validResendSender(sender string) bool {
	parsed, err := mail.ParseAddress(strings.TrimSpace(sender))
	return err == nil && parsed.Address != ""
}

func clearCredential(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func validReceiptFacts(receipt payments.ReceiptEmail) bool {
	if len(receipt.EventID) != 36 || strings.TrimSpace(receipt.PlanName) == "" || len(strings.TrimSpace(receipt.PlanName)) > 200 || len(strings.TrimSpace(receipt.Reference)) == 0 || len(strings.TrimSpace(receipt.Reference)) > 200 || receipt.AmountMinor <= 0 || receipt.StartsAt.IsZero() || receipt.ExpiresAt.IsZero() || !receipt.ExpiresAt.After(receipt.StartsAt) {
		return false
	}
	currency := strings.TrimSpace(receipt.Currency)
	if len(currency) != 3 {
		return false
	}
	for _, character := range currency {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func otpMessage(purpose auth.OTPPurpose, code string) (subject, text string, ok bool) {
	if len(code) != auth.OTPCodeLength {
		return "", "", false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return "", "", false
		}
	}
	switch purpose {
	case auth.OTPEmailVerification:
		return "Verify your NetCore email", "Your NetCore email verification code is " + code + ". This code expires in 10 minutes.", true
	case auth.OTPPasswordReset:
		return "Reset your NetCore password", "Your NetCore password reset code is " + code + ". This code expires in 10 minutes.", true
	default:
		return "", "", false
	}
}
