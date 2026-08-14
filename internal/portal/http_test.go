package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

const portalTestTenantID = "11111111-1111-4111-8111-111111111111"
const portalTestUserID = "22222222-2222-4222-8222-222222222222"

type memoryLimiter struct {
	allowed bool
	err     error
	keys    []string
}

func (l *memoryLimiter) AllowSlidingWindow(_ context.Context, key string, _ int64, _ time.Duration) (bool, error) {
	l.keys = append(l.keys, key)
	return l.allowed, l.err
}

func newTestHTTP(t *testing.T, store HandoffStore, limiter HandoffLimiter) *HTTP {
	t.Helper()
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTP(service, limiter, []string{"http://portal.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func portalRequest(t *testing.T) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/portal/handoff", strings.NewReader(`{"client_mac":"AA:BB:CC:DD:EE:FF","nas_address":"10.10.0.1","hotspot_login_url":"http://10.10.0.1/login"}`))
	return request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{
		TenantID: portalTestTenantID,
		UserID:   portalTestUserID,
	}))
}

func TestIssueReturnsHandoffForAuthenticatedEntitledUser(t *testing.T) {
	store := &memoryStore{}
	limiter := &memoryLimiter{allowed: true}
	handler := newTestHTTP(t, store, limiter)
	response := httptest.NewRecorder()

	handler.issue(response, portalRequest(t))

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("handoff security headers = %+v", response.Header())
	}
	if len(limiter.keys) != 2 || !strings.HasPrefix(limiter.keys[0], "portal:handoff:account:") || !strings.HasPrefix(limiter.keys[1], "portal:handoff:device:") {
		t.Fatalf("unexpected limiter keys: %v", limiter.keys)
	}
	var body handoffResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body.RedirectURL, "http://10.10.0.1/login?") || !strings.Contains(body.RedirectURL, "username=") || body.ExpiresAt.IsZero() {
		t.Fatalf("unexpected response: %+v", body)
	}
	for _, forbidden := range []string{"subscription", "client_mac", "nas_address", "token_hash", "user_id"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, response.Body)
		}
	}
}

func TestIssueReturnsPlanChoiceWhenNoEntitlementExists(t *testing.T) {
	limiter := &memoryLimiter{allowed: true}
	handler := newTestHTTP(t, &memoryStore{err: ErrNoActivePlan}, limiter)
	response := httptest.NewRecorder()

	handler.issue(response, portalRequest(t))

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "NO_ACTIVE_PLAN") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestIssueFailsClosedWhenLimiterUnavailable(t *testing.T) {
	handler := newTestHTTP(t, &memoryStore{}, &memoryLimiter{err: errors.New("redis down")})
	response := httptest.NewRecorder()

	handler.issue(response, portalRequest(t))

	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "PORTAL_UNAVAILABLE") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestIssueRejectsCrossSiteBrowserOrigin(t *testing.T) {
	handler := newTestHTTP(t, &memoryStore{}, &memoryLimiter{allowed: true})
	request := portalRequest(t)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()

	handler.issue(response, request)

	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "CSRF_REJECTED") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}
