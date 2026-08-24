package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testLimiter struct {
	allowed bool
	err     error
	keys    []string
}

func (l *testLimiter) AllowSlidingWindow(_ context.Context, key string, _ int64, _ time.Duration) (bool, error) {
	l.keys = append(l.keys, key)
	return l.allowed, l.err
}

func newTestHTTP(t *testing.T) (*HTTP, *memoryStore, *testLimiter) {
	t.Helper()
	service, store := newTestService(t)
	limiter := &testLimiter{allowed: true}
	h, err := NewHTTP(service, limiter, false, []string{"https://portal.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h, store, limiter
}

func TestLoginSuccessSetsHTTPOnlySessionCookie(t *testing.T) {
	h, _, limiter := newTestHTTP(t)
	mux := http.NewServeMux()
	h.Routes(mux)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"tenant":"example","identifier":"admin@example.com","password":"correct password"}`))
	req.RemoteAddr = "203.0.113.9:4040"
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly || cookies[0].Secure {
		t.Fatalf("unexpected session cookie: %+v", cookies)
	}
	if len(limiter.keys) != 2 || strings.Contains(limiter.keys[0], "admin@example.com") {
		t.Fatalf("rate limit keys leaked raw account data: %v", limiter.keys)
	}
}

func TestLoginForwardsMFACodeBeforeSettingSessionCookie(t *testing.T) {
	service, _ := newTestService(t)
	verifier := &testMFAVerifier{}
	if err := service.RequireMFA(verifier); err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTP(service, &testLimiter{allowed: true}, false, []string{"https://portal.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"tenant":"example","identifier":"admin@example.com","password":"correct password","mfa_code":"123456"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || verifier.calls != 1 || verifier.code != "123456" {
		t.Fatalf("status=%d verifier_calls=%d code=%q body=%s", response.Code, verifier.calls, verifier.code, response.Body.String())
	}
}

func TestLoginInvalidMFAIsIndistinguishableAtHTTPBoundary(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.RequireMFA(&testMFAVerifier{err: ErrInvalidMFA}); err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTP(service, &testLimiter{allowed: true}, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"tenant":"example","identifier":"admin@example.com","password":"correct password","mfa_code":"000000"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "INVALID_CREDENTIALS") {
		t.Fatalf("invalid MFA response status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestClientIPTrustsForwardedAddressOnlyFromConfiguredProxy(t *testing.T) {
	service, _ := newTestService(t)
	h, err := NewHTTP(service, &testLimiter{allowed: true}, false, nil, []string{"172.30.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	proxied := httptest.NewRequest(http.MethodGet, "/", nil)
	proxied.RemoteAddr = "172.30.0.2:8080"
	proxied.Header.Set("X-Forwarded-For", "198.51.100.42")
	if got := h.clientIP(proxied); got != "198.51.100.42" {
		t.Fatalf("trusted proxy client IP = %q", got)
	}
	direct := httptest.NewRequest(http.MethodGet, "/", nil)
	direct.RemoteAddr = "203.0.113.8:8080"
	direct.Header.Set("X-Forwarded-For", "198.51.100.42")
	if got := h.clientIP(direct); got != "203.0.113.8" {
		t.Fatalf("direct client spoofed X-Forwarded-For: %q", got)
	}
}

func TestLoginFailureResponseDoesNotEnumerate(t *testing.T) {
	h, store, _ := newTestHTTP(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	request := func(identifier, password string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"tenant":"example","identifier":"`+identifier+`","password":"`+password+`"}`))
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		return res
	}
	wrongPassword := request("admin@example.com", "wrong")
	store.userFound = false
	unknownUser := request("missing@example.com", "wrong")
	if wrongPassword.Code != http.StatusUnauthorized || unknownUser.Code != http.StatusUnauthorized || wrongPassword.Body.String() != unknownUser.Body.String() {
		t.Fatalf("responses reveal account existence: wrong=%d/%q unknown=%d/%q", wrongPassword.Code, wrongPassword.Body, unknownUser.Code, unknownUser.Body)
	}
}

func TestLoginRejectsCrossOriginRequest(t *testing.T) {
	h, _, _ := newTestHTTP(t)
	mux := http.NewServeMux()
	h.Routes(mux)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"tenant":"example","identifier":"admin@example.com","password":"correct password"}`))
	req.Header.Set("Origin", "https://attacker.example")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestLoginFailsClosedWhenLimiterFails(t *testing.T) {
	h, _, limiter := newTestHTTP(t)
	limiter.err = errors.New("redis unavailable")
	mux := http.NewServeMux()
	h.Routes(mux)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"tenant":"example","identifier":"admin@example.com","password":"correct password"}`))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestProtectedRouteRequiresSession(t *testing.T) {
	h, _, _ := newTestHTTP(t)
	mux := http.NewServeMux()
	h.Routes(mux)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestLogoutRejectsCrossOriginSessionRequest(t *testing.T) {
	h, _, _ := newTestHTTP(t)
	mux := http.NewServeMux()
	h.Routes(mux)
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testTenantID + ".aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	req.Header.Set("Origin", "https://attacker.example")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestRequireAllowedOriginRejectsCrossSiteWrites(t *testing.T) {
	h, _, _ := newTestHTTP(t)
	called := false
	protected := h.RequireAllowedOrigin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plans", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()

	protected.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || called {
		t.Fatalf("status=%d called=%t body=%s", response.Code, called, response.Body.String())
	}
}

func TestRequirePermissionUsesExplicitPermission(t *testing.T) {
	called := false
	h := RequirePermission("customer.read", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), principalContextKey{}, Principal{Permissions: map[string]struct{}{"customer.read": {}}})
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
	if !called {
		t.Fatal("permissioned request did not reach handler")
	}
}
