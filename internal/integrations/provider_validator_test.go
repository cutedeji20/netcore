package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

func TestHTTPProviderValidatorSendsResendConfirmationToAuthenticatedAdministrator(t *testing.T) {
	var request struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer re_test_dashboard_key" {
			t.Fatalf("request = %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	validator, err := NewHTTPProviderValidator(client)
	if err != nil {
		t.Fatal(err)
	}
	validator.resendBaseURL = server.URL
	err = validator.Validate(context.Background(), ConfigureInput{
		Principal: auth.Principal{TenantID: "tenant-a", UserID: "staff-a", Email: "administrator@example.test"},
		Provider:  ProviderResend, Credential: []byte("re_test_dashboard_key"), SenderEmail: "NetCore <access@example.test>",
	})
	if err != nil || request.From != "NetCore <access@example.test>" || request.To != "administrator@example.test" {
		t.Fatalf("Validate error=%v request=%#v", err, request)
	}
}

func TestHTTPProviderValidatorChecksPaystackBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/balance" || r.Header.Get("Authorization") != "Bearer sk_test_dashboard_key" {
			t.Fatalf("request = %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"status":true,"data":[{"currency":"NGN","balance":0}]}`))
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	validator, err := NewHTTPProviderValidator(client)
	if err != nil {
		t.Fatal(err)
	}
	validator.paystackBaseURL = server.URL
	err = validator.Validate(context.Background(), ConfigureInput{
		Principal: auth.Principal{TenantID: "tenant-a", UserID: "staff-a", Email: "administrator@example.test"},
		Provider:  ProviderPaystack, Credential: []byte("sk_test_dashboard_key"), PaystackMode: "TEST",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
