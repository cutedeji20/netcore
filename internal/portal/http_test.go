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
	limits  []int64
}

type guestRedeemerStub struct { calls int; err error }
func (s *guestRedeemerStub) RedeemForDevice(_ context.Context, _, _, _, _ string) (string,error) { s.calls++; return "33333333-3333-4333-8333-333333333333",s.err }

func TestGuestVoucherHandoffIsGatedAndDoesNotExposeCode(t *testing.T) {
	store := &memoryStore{}
	h := newTestHTTP(t, store, &memoryLimiter{allowed:true})
	code := "NC-ABCDEF-GHJKLM-NPQRST"
	request := func() *http.Request { return httptest.NewRequest(http.MethodPost,"/api/v1/portal/voucher-handoff",strings.NewReader(`{"code":"`+code+`","client_mac":"AA:BB:CC:DD:EE:FF","nas_address":"10.10.0.1","hotspot_login_url":"http://10.10.0.1/login"}`)) }
	response := httptest.NewRecorder()
	h.guestIssue(response,request())
	if response.Code != http.StatusNotFound { t.Fatalf("disabled status=%d",response.Code) }
	redeemer := &guestRedeemerStub{}
	h.ConfigureGuestVoucher(redeemer, portalTestTenantID, true)
	badOrigin := request(); badOrigin.Header.Set("Origin","https://other.example")
	response = httptest.NewRecorder(); h.guestIssue(response,badOrigin)
	if response.Code != http.StatusForbidden || redeemer.calls != 0 { t.Fatalf("bad origin status=%d calls=%d",response.Code,redeemer.calls) }
	response = httptest.NewRecorder(); h.guestIssue(response,request())
	if response.Code != http.StatusCreated || redeemer.calls != 1 || store.record.UserID != "" || store.record.SubscriptionID != "33333333-3333-4333-8333-333333333333" || strings.Contains(response.Body.String(),code) || strings.Contains(response.Body.String(),"client_mac") { t.Fatalf("guest response=%d body=%s record=%+v",response.Code,response.Body.String(),store.record) }
}

func (l *memoryLimiter) AllowSlidingWindow(_ context.Context, key string, limit int64, _ time.Duration) (bool, error) {
	l.keys = append(l.keys, key)
	l.limits = append(l.limits, limit)
	return l.allowed, l.err
}

func TestGuestVoucherLimitsArePerIPCodeAndDevice(t *testing.T) {
	h := newTestHTTP(t, &memoryStore{}, &memoryLimiter{allowed: true})
	l := h.limiter.(*memoryLimiter)
	if err := h.limitGuestIssue(context.Background(), portalTestTenantID, "198.51.100.1", "aabbccddeeff", "NC-ABCDEF-GHJKLM-NPQRST"); err != nil {
		t.Fatal(err)
	}
	if len(l.keys) != 3 || !strings.HasPrefix(l.keys[0], "portal:voucher:ip:") || !strings.HasPrefix(l.keys[1], "portal:voucher:code:") || !strings.HasPrefix(l.keys[2], "portal:voucher:device:") {
		t.Fatalf("keys = %v", l.keys)
	}
	if l.limits[0] != 30 || l.limits[1] != 5 || l.limits[2] != 10 {
		t.Fatalf("limits = %v", l.limits)
	}
	firstIPKey := l.keys[0]
	if err := h.limitGuestIssue(context.Background(), portalTestTenantID, "198.51.100.2", "aabbccddeeff", "NC-ABCDEF-GHJKLM-NPQRST"); err != nil {
		t.Fatal(err)
	}
	if l.keys[3] == firstIPKey {
		t.Fatal("different IPs shared an attempt bucket")
	}
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

func TestIssueExplainsActivePlanBoundToAnotherDevice(t *testing.T) {
	handler := newTestHTTP(t, &memoryStore{err: ErrDeviceMismatch}, &memoryLimiter{allowed: true})
	response := httptest.NewRecorder()
	handler.issue(response, portalRequest(t))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "PLAN_DEVICE_MISMATCH") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestDeviceReplacementEndpointsStayHiddenWhenFlagIsOff(t *testing.T) {
	handler := newTestHTTP(t, &memoryStore{}, &memoryLimiter{allowed: true})
	for _, path := range []string{"/api/v1/portal/device-replacement/request", "/api/v1/portal/device-replacement/verify"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: portalTestTenantID, UserID: portalTestUserID}))
		response := httptest.NewRecorder()
		if strings.HasSuffix(path, "/request") {
			handler.requestReplacement(response, request)
		} else {
			handler.verifyReplacement(response, request)
		}
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
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
