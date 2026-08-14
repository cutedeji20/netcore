package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewServesDashboard(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "NetCore") {
		t.Fatal("dashboard markup was not served")
	}
	if !strings.Contains(body, "/live-customers.js") || !strings.Contains(body, "/live-subscriptions.js") || !strings.Contains(body, "/live-plans.js") || !strings.Contains(body, "/live-sessions.js") || !strings.Contains(body, "/live-billing.js") || !strings.Contains(body, "/live-network.js") || !strings.Contains(body, "/live-vouchers.js") || !strings.Contains(body, "/live-team.js") || !strings.Contains(body, "/live-security.js") || !strings.Contains(body, "/live-automations.js") || !strings.Contains(body, "/live-workspace.js") {
		t.Fatal("live data adapters were not referenced")
	}
}

func TestPreviewRevalidatesShellAssets(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/app.css", "/app.js"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if got := response.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s Cache-Control = %q, want no-cache", path, got)
		}
	}
}

func TestPreviewCommandPaletteClosesOnPageShow(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if !strings.Contains(response.Body.String(), "resetCommandOnLoad") || !strings.Contains(response.Body.String(), "window.addEventListener(\"pageshow\", resetCommandOnLoad)") {
		t.Fatal("command palette does not reset on page show")
	}
}

func TestPreviewHiddenCommandPaletteIsNotRendered(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.css", nil))
	if !strings.Contains(response.Body.String(), ".modal-backdrop[hidden]{display:none}") {
		t.Fatal("hidden command palette CSS rule was not served")
	}
}

func TestPreviewServesAutomationAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-automations.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/automations") {
		t.Fatal("automation API adapter was not served")
	}
}

func TestPreviewServesWorkspaceAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-workspace.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/workspace/settings") {
		t.Fatal("workspace API adapter was not served")
	}
}

func TestPreviewServesSecurityAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-security.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/security/events") {
		t.Fatal("security API adapter was not served")
	}
}

func TestPreviewServesCaptivePortal(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/portal.html", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("portal security headers = %+v", response.Header())
	}
	body := response.Body.String()
	if !strings.Contains(body, "doesn’t have an active internet plan") || !strings.Contains(body, "/portal.js") {
		t.Fatal("captive portal markup was not served")
	}
}

func TestPreviewServesTeamAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-team.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/team/members") {
		t.Fatal("team API adapter was not served")
	}
}

func TestPreviewServesVoucherAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-vouchers.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/vouchers/batches") {
		t.Fatal("voucher API adapter was not served")
	}
}

func TestPreviewServesNetworkAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-network.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/network/routers") {
		t.Fatal("network API adapter was not served")
	}
}

func TestPreviewServesBillingAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-billing.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/billing/transactions") {
		t.Fatal("billing API adapter was not served")
	}
}

func TestPreviewServesSessionAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-sessions.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/sessions") {
		t.Fatal("session API adapter was not served")
	}
}

func TestPreviewServesPlanAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-plans.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/plans") {
		t.Fatal("plan API adapter was not served")
	}
}

func TestPreviewServesSubscriptionAdapter(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-subscriptions.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "/api/v1/subscriptions") {
		t.Fatal("subscription API adapter was not served")
	}
}
