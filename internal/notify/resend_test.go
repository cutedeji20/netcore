package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/payments"
)

type testSecretResolver map[string]string

func (r testSecretResolver) Resolve(_ context.Context, reference string) (string, error) {
	return r[reference], nil
}

func TestResendNotifierSendsEmailVerificationCode(t *testing.T) {
	var request struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/emails" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer re_test_secret" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Fatal("User-Agent was not set")
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"d20e52a7-a435-4cc2-8061-9e82fe98bbf3"}`))
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second
	notifier, err := NewResendNotifier(testSecretResolver{"email.resend.api_key": "re_test_secret"}, "email.resend.api_key", "NetCore <access@notify.durabledatahubs.com>", client)
	if err != nil {
		t.Fatal(err)
	}
	notifier.baseURL = server.URL

	if err := notifier.SendOTP(context.Background(), auth.OTPEmailVerification, "customer@example.com", "482913", time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("SendOTP: %v", err)
	}
	if request.From != "NetCore <access@notify.durabledatahubs.com>" || request.To != "customer@example.com" {
		t.Fatalf("sender/recipient = %#v", request)
	}
	if request.Subject != "Verify your NetCore email" {
		t.Fatalf("subject = %q", request.Subject)
	}
	if request.Text == "" || !strings.Contains(request.Text, "482913") || !strings.Contains(request.Text, "10 minutes") {
		t.Fatalf("verification email body = %q", request.Text)
	}
}

func TestResendNotifierSendsPasswordResetCode(t *testing.T) {
	var request struct {
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second
	notifier, err := NewResendNotifier(testSecretResolver{"email.resend.api_key": "re_test_secret"}, "email.resend.api_key", "NetCore <access@notify.durabledatahubs.com>", client)
	if err != nil {
		t.Fatal(err)
	}
	notifier.baseURL = server.URL

	if err := notifier.SendOTP(context.Background(), auth.OTPPasswordReset, "customer@example.com", "891204", time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("SendOTP: %v", err)
	}
	if request.Subject != "Reset your NetCore password" || !strings.Contains(request.Text, "891204") {
		t.Fatalf("password reset email = %#v", request)
	}
}

func TestResendNotifierSendsVerifiedPaymentReceipt(t *testing.T) {
	var request struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/emails" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "payment.receipt.requested/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
			t.Fatalf("Idempotency-Key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second
	notifier, err := NewResendNotifier(testSecretResolver{"email.resend.api_key": "re_test_secret"}, "email.resend.api_key", "NetCore <access@notify.durabledatahubs.com>", client)
	if err != nil {
		t.Fatal(err)
	}
	notifier.baseURL = server.URL
	receipt := payments.ReceiptEmail{
		EventID:     "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		To:          "customer@example.com",
		PlanName:    "Weekly access",
		Reference:   "pay_12345678901234567890123456789012",
		AmountMinor: 250000,
		Currency:    "NGN",
		StartsAt:    time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC),
		ExpiresAt:   time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC),
	}
	if err := notifier.SendPaymentReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("SendPaymentReceipt: %v", err)
	}
	if request.From != "NetCore <access@notify.durabledatahubs.com>" || request.To != "customer@example.com" || request.Subject != "Your NetCore payment receipt" {
		t.Fatalf("receipt request = %#v", request)
	}
	for _, want := range []string{"Weekly access", "2500.00 NGN", "pay_12345678901234567890123456789012", "2026-08-24", "2026-08-31"} {
		if !strings.Contains(request.Text, want) {
			t.Fatalf("receipt text missing %q: %q", want, request.Text)
		}
	}
	for _, forbidden := range []string{"http://", "https://", "re_test_secret", "card"} {
		if strings.Contains(strings.ToLower(request.Text), strings.ToLower(forbidden)) {
			t.Fatalf("receipt text leaked %q: %q", forbidden, request.Text)
		}
	}
}
