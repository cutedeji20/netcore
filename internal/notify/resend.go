// Package notify contains production notification adapters. Providers receive
// only the destination and message required for delivery; application secrets
// stay behind logical SecretStore references.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/payments"
	"github.com/netcore-isp/netcore/pkg/money"
)

const resendEmailsURL = "https://api.resend.com/emails"

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
		secrets: secrets, secretRef: strings.TrimSpace(secretRef), from: strings.TrimSpace(from), client: client, baseURL: resendEmailsURL,
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
