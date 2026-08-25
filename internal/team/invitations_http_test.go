package team

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/pkg/crypto/totp"
)

// This catches a public endpoint regression where malformed credentials reveal
// a distinct parsing response from expired, revoked, or redeemed invitations.
func TestPrepareInvitationUsesGenericInvalidResponse(t *testing.T) {
	handler, err := NewHTTP(&memoryStore{}, 25, 100)
	if err != nil {
		t.Fatal(err)
	}
	service := newInvitationService(t, acceptingStepUp{}, &recordingSender{})
	if err := handler.ConfigureInvitations(service, acceptingInvitationLimiter{}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/staff-invitations/prepare", strings.NewReader(`{"token":"malformed","extra":true}`))
	request.RemoteAddr = "192.0.2.1:1234"
	response := httptest.NewRecorder()
	handler.prepareInvitation(&auth.HTTP{})(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_INVITATION") || strings.Contains(response.Body.String(), "unknown") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q", response.Header().Get("Cache-Control"))
	}
}

func TestCompleteInvitationAcceptsMFACodeJSONField(t *testing.T) {
	store := &memoryInvitationStore{}
	sender := &recordingSender{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, sender)
	if _, err := service.Invite(context.Background(), InviteInput{Principal: administrator(), Email: "ops@example.test", Role: RoleOperations, Password: "correct password", MFACode: "123456"}); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(strings.Split(sender.url, "#")[1], "token=")
	setup, err := service.PrepareAcceptance(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(setup.ManualKey, time.Now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTP(&memoryStore{}, 25, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.ConfigureInvitations(service, acceptingInvitationLimiter{}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"token": token, "password": "a long enough invitation password", "mfa_code": code})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/staff-invitations/complete", strings.NewReader(string(body)))
	req.RemoteAddr = "192.0.2.1:1234"
	res := httptest.NewRecorder()
	handler.completeInvitation(&auth.HTTP{})(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache=%q", res.Header().Get("Cache-Control"))
	}
}

func TestInvitationProjectionRedactsCredentialMaterial(t *testing.T) {
	inv := Invitation{ID: "33333333-3333-4333-8333-333333333333", Email: "ops@example.test", Role: RoleOperations, Status: "PENDING", ExpiresAt: time.Now(), MFA: auth.MFASecretEnvelope{Ciphertext: []byte("secret"), KEKKeyID: "key-id"}}
	encoded, err := json.Marshal(invitationResponse{Invitation: invitationProjection(inv)})
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if strings.Contains(body, "secret") || strings.Contains(body, "key-id") || strings.Contains(body, "digest") {
		t.Fatalf("sensitive invitation projection: %s", body)
	}
}

func TestListInvitationProjectionsReturnsOnlySafePendingRecords(t *testing.T) {
	store := &memoryInvitationStore{}
	service := newInvitationServiceWithStore(t, store, acceptingStepUp{}, &recordingSender{})
	if _, err := service.Invite(context.Background(), InviteInput{Principal: administrator(), Email: "ops@example.test", Role: RoleOperations, Password: "correct password", MFACode: "123456"}); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTP(&memoryStore{}, 25, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.ConfigureInvitations(service, acceptingInvitationLimiter{}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/team/invitations", nil)
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), administrator()))
	response := httptest.NewRecorder()
	handler.listInvitations(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ops@example.test") || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

type acceptingInvitationLimiter struct{}

func (acceptingInvitationLimiter) AllowSlidingWindow(context.Context, string, int64, time.Duration) (bool, error) {
	return true, nil
}
