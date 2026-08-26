package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/integrations"
	"github.com/netcore-isp/netcore/internal/payments"
)

type testTenantResendResolver struct {
	tenantID string
	provider integrations.Provider
}

type recordingTransport struct {
	request *http.Request
}

type failingTenantResendResolver struct{}

func (failingTenantResendResolver) Resolve(context.Context, string, integrations.Provider) ([]byte, integrations.CredentialMetadata, error) {
	return nil, integrations.CredentialMetadata{}, errors.New("key vault unwrap failed")
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("outbound connection failed")
}

func (t *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.request = request.Clone(request.Context())
	return &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Request:    request,
	}, nil
}

func (r *testTenantResendResolver) Resolve(_ context.Context, tenantID string, provider integrations.Provider) ([]byte, integrations.CredentialMetadata, error) {
	r.tenantID = tenantID
	r.provider = provider
	return []byte("re_dashboard_managed_key"), integrations.CredentialMetadata{SenderEmail: "DataHub <hotspot@example.test>"}, nil
}

func TestTenantResendNotifierLoadsDashboardCredentialForItsTenant(t *testing.T) {
	// This fails if public account email falls back to a deployment key or can
	// send with a credential selected outside the portal tenant.
	var request struct {
		From string `json:"from"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer re_dashboard_managed_key" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	resolver := &testTenantResendResolver{}
	client := server.Client()
	client.Timeout = time.Second
	notifier, err := NewTenantResendNotifier(resolver, "tenant-data-hub", client)
	if err != nil {
		t.Fatal(err)
	}
	notifier.baseURL = server.URL
	if err := notifier.SendOTP(context.Background(), auth.OTPEmailVerification, "customer@example.com", "482913", time.Now().Add(10*time.Minute)); err != nil {
		t.Fatalf("SendOTP: %v", err)
	}
	if resolver.tenantID != "tenant-data-hub" || resolver.provider != integrations.ProviderResend {
		t.Fatalf("resolver scope = %q %q", resolver.tenantID, resolver.provider)
	}
	if request.From != "DataHub <hotspot@example.test>" {
		t.Fatalf("sender = %q", request.From)
	}
}

func TestTenantResendNotifierPostsOTPToResendEmailsEndpoint(t *testing.T) {
	// This fails if the notifier treats the /emails endpoint as its base URL
	// and appends /emails again, which makes public account verification fail.
	transport := &recordingTransport{}
	notifier, err := NewTenantResendNotifier(&testTenantResendResolver{}, "tenant-data-hub", &http.Client{Transport: transport, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	if err := notifier.SendOTP(context.Background(), auth.OTPEmailVerification, "customer@example.com", "482913", time.Now().Add(10*time.Minute)); err != nil {
		t.Fatalf("SendOTP: %v", err)
	}
	if transport.request == nil {
		t.Fatal("Resend request was not made")
	}
	if got, want := transport.request.Method, http.MethodPost; got != want {
		t.Fatalf("Resend OTP method = %q, want %q", got, want)
	}
	if got, want := transport.request.URL.String(), "https://api.resend.com/emails"; got != want {
		t.Fatalf("Resend OTP URL = %q, want %q", got, want)
	}
}

func TestTenantResendNotifierLogsSafeDeliveryFailureStages(t *testing.T) {
	testCases := []struct {
		name       string
		notifier   func(*testing.T) *TenantResendNotifier
		wantStage  string
		wantStatus string
	}{
		{
			name: "credential resolution",
			notifier: func(t *testing.T) *TenantResendNotifier {
				notifier, err := NewTenantResendNotifier(failingTenantResendResolver{}, "tenant-data-hub", &http.Client{Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				return notifier
			},
			wantStage: "credential_resolution",
		},
		{
			name: "request creation",
			notifier: func(t *testing.T) *TenantResendNotifier {
				notifier, err := NewTenantResendNotifier(&testTenantResendResolver{}, "tenant-data-hub", &http.Client{Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				notifier.baseURL = "://invalid"
				return notifier
			},
			wantStage: "request_creation",
		},
		{
			name: "transport",
			notifier: func(t *testing.T) *TenantResendNotifier {
				notifier, err := NewTenantResendNotifier(&testTenantResendResolver{}, "tenant-data-hub", &http.Client{Transport: failingTransport{}, Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				return notifier
			},
			wantStage: "transport_unavailable",
		},
		{
			name: "provider rejection",
			notifier: func(t *testing.T) *TenantResendNotifier {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "credential and customer data must not appear in logs", http.StatusForbidden)
				}))
				t.Cleanup(server.Close)
				client := server.Client()
				client.Timeout = time.Second
				notifier, err := NewTenantResendNotifier(&testTenantResendResolver{}, "tenant-data-hub", client)
				if err != nil {
					t.Fatal(err)
				}
				notifier.baseURL = server.URL
				return notifier
			},
			wantStage:  "provider_rejected",
			wantStatus: `"provider_status":403`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			notifier := testCase.notifier(t)
			if err := notifier.SendOTP(context.Background(), auth.OTPEmailVerification, "customer@example.com", "482913", time.Now().Add(10*time.Minute)); err == nil {
				t.Fatal("SendOTP succeeded")
			}

			entry := output.String()
			if !strings.Contains(entry, `"failure_stage":"`+testCase.wantStage+`"`) || (testCase.wantStatus != "" && !strings.Contains(entry, testCase.wantStatus)) {
				t.Fatalf("diagnostic log = %s", entry)
			}
			for _, secret := range []string{"customer@example.com", "482913", "re_dashboard_managed_key", "credential and customer data"} {
				if strings.Contains(entry, secret) {
					t.Fatalf("diagnostic log contains protected value %q: %s", secret, entry)
				}
			}
		})
	}
}

func TestTenantResendNotifierLoadsDashboardCredentialForPaymentReceipt(t *testing.T) {
	// This fails if the receipt worker sends with a deployment secret instead
	// of the active tenant's dashboard-managed Resend key and sender.
	var request struct {
		From string `json:"from"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer re_dashboard_managed_key" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "payment.receipt.requested/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
			t.Fatalf("idempotency key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second
	resolver := &testTenantResendResolver{}
	notifier, err := NewTenantResendNotifier(resolver, "tenant-data-hub", client)
	if err != nil {
		t.Fatal(err)
	}
	notifier.baseURL = server.URL
	receipt := payments.ReceiptEmail{
		EventID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", To: "customer@example.com", PlanName: "Weekly access",
		Reference: "pay_12345678901234567890123456789012", AmountMinor: 250000, Currency: "NGN",
		StartsAt: time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC),
	}
	if err := notifier.SendPaymentReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("SendPaymentReceipt: %v", err)
	}
	if resolver.tenantID != "tenant-data-hub" || resolver.provider != integrations.ProviderResend || request.From != "DataHub <hotspot@example.test>" {
		t.Fatalf("receipt resolver/sender = %q %q %q", resolver.tenantID, resolver.provider, request.From)
	}
}

func TestTenantResendNotifierSendsInvitationWithInvitationScopedIdempotency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Idempotency-Key"); got != "staff.invitation/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
			t.Fatalf("idempotency key = %q", got)
		}
		if r.Referer() != "" {
			t.Fatalf("referer = %q", r.Referer())
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	notifier, err := NewTenantResendNotifier(&testTenantResendResolver{}, "tenant-data-hub", client)
	if err != nil {
		t.Fatal(err)
	}
	notifier.baseURL = server.URL
	if err := notifier.SendStaffInvitationWithID(context.Background(), "staff@example.com", "https://app.example.test/invitations/accept#token=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", time.Now().Add(time.Hour), "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
}

func TestTenantResendNotifierRejectsMalformedInvitationID(t *testing.T) {
	notifier, err := NewTenantResendNotifier(&testTenantResendResolver{}, "tenant-data-hub", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.SendStaffInvitationWithID(context.Background(), "staff@example.com", "https://app.example.test/accept#token=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", time.Now().Add(time.Hour), "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"); err == nil {
		t.Fatal("malformed id was accepted")
	}
}
