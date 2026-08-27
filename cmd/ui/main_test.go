package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardServesLockedConfigurationByDefault(t *testing.T) {
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
	if !strings.Contains(body, "NetCore") || !strings.Contains(body, "/admin-config.js") {
		t.Fatal("dashboard markup was not served")
	}
	if strings.Contains(body, "/live-customers.js") || strings.Contains(body, "/live-subscriptions.js") {
		t.Fatal("live data adapters must not load before an authorised session")
	}
}

func TestDashboardServesNoStoreLockedConfig(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin-config.js", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
	if !strings.Contains(response.Body.String(), `"mode":"locked"`) || strings.Contains(response.Body.String(), "127.0.0.1") {
		t.Fatalf("unsafe default admin configuration: %s", response.Body.String())
	}
}

func TestLoadAdminConfigAllowsOnlyValidLiveTenant(t *testing.T) {
	config, err := loadAdminConfig(func(key string) string {
		if key == "NETCORE_UI_MODE" {
			return "live"
		}
		return "lagos-hub"
	})
	if err != nil || config.Mode != "live" || config.Tenant != "lagos-hub" {
		t.Fatalf("loadAdminConfig() = %#v, %v", config, err)
	}
	if _, err := loadAdminConfig(func(key string) string {
		if key == "NETCORE_UI_MODE" {
			return "live"
		}
		return "Not a slug"
	}); err == nil {
		t.Fatal("invalid production tenant configuration was accepted")
	}
}

func TestLoadPortalConfigUsesSameOriginLiveModeWithoutTenant(t *testing.T) {
	config, err := loadPortalConfig(func(key string) string {
		if key == "NETCORE_UI_MODE" {
			return "live"
		}
		return "lagos-hub"
	})
	if err != nil || config.Mode != "live" || config.APIBase != "" || !config.AccountsEnabled || !config.PaymentsEnabled {
		t.Fatalf("loadPortalConfig() = %#v, %v", config, err)
	}
	payload := httptest.NewRecorder()
	servePortalConfig(payload, config)
	if payload.Code != http.StatusOK || strings.Contains(payload.Body.String(), "tenant") || !strings.Contains(payload.Body.String(), `"mode":"live"`) {
		t.Fatalf("unsafe portal configuration: status=%d body=%s", payload.Code, payload.Body)
	}
}

func TestLoadPortalConfigDoesNotRequireProviderEnvironmentFlags(t *testing.T) {
	config, err := loadPortalConfig(func(key string) string {
		if key == "NETCORE_UI_MODE" || key == "NETCORE_TENANT_SLUG" {
			return "live"
		}
		return "disabled"
	})
	if err != nil || !config.AccountsEnabled || !config.PaymentsEnabled {
		t.Fatalf("loadPortalConfig() = %#v, %v; live portal must follow dashboard-managed providers", config, err)
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

func TestDashboardAssetsHaveBrowserSecurityHeaders(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", response.Header().Get("X-Content-Type-Options"))
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", response.Header().Get("Content-Security-Policy"))
	}
}

func TestStaffInvitationPageHasNoStoreNoReferrerSameOriginCSP(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/staff-invite.html", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	const expectedCSP = "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; connect-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:"
	if csp := response.Header().Get("Content-Security-Policy"); csp != expectedCSP {
		t.Fatalf("CSP = %q, want %q", csp, expectedCSP)
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

func TestPreviewServesRefactoredAdaptersWithNamedSharedConfiguration(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}

	for _, adapter := range []struct {
		path string
		page string
	}{
		{path: "/live-automations.js", page: "automations"},
		{path: "/live-security.js", page: "security"},
		{path: "/live-vouchers.js", page: "vouchers"},
		{path: "/live-network.js", page: "network"},
		{path: "/live-billing.js", page: "billing"},
		{path: "/live-sessions.js", page: "sessions"},
		{path: "/live-subscriptions.js", page: "subscriptions"},
	} {
		t.Run(adapter.page, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, adapter.path, nil))

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			if !strings.Contains(response.Body.String(), `NetCoreLiveListConfig.get("`+adapter.page+`")`) {
				t.Fatalf("adapter does not consume the %q shared list configuration", adapter.page)
			}
		})
	}
}

func TestPreviewServesLiveListConfigPublicHelper(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-list-config.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "NetCoreLiveListConfig = api") || !strings.Contains(body, "return { get: get }") {
		t.Fatal("live list configuration helper was not served")
	}
}

func TestPreviewServesLiveListControlsPublicHelpers(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live-list-controls.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "NetCoreLiveListControls = api") || !strings.Contains(body, "requestURL: requestURL") {
		t.Fatal("live list controls helpers were not served")
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

func TestPortalUsesThePublishedPlanCatalogueInsteadOfHardcodedPlans(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/portal.js", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "/api/v1/portal/catalogue") || !strings.Contains(body, "renderCatalogue") || strings.Contains(body, "data-plan=\"Day Pass\"") {
		t.Fatalf("portal does not use the public catalogue: %s", body)
	}
}

func TestPortalServesCustomerRegistrationAndEmailVerificationJourney(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	html := httptest.NewRecorder()
	handler.ServeHTTP(html, httptest.NewRequest(http.MethodGet, "/portal.html", nil))
	if html.Code != http.StatusOK || !strings.Contains(html.Body.String(), `id="portal-register"`) || !strings.Contains(html.Body.String(), `id="portal-verify-email"`) {
		t.Fatalf("portal account forms missing: status=%d body=%s", html.Code, html.Body)
	}
	script := httptest.NewRecorder()
	handler.ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/portal.js", nil))
	for _, endpoint := range []string{"/portal/auth/register", "/portal/auth/verify-email"} {
		if !strings.Contains(script.Body.String(), endpoint) {
			t.Fatalf("portal does not call %q", endpoint)
		}
	}
}

func TestPortalServesPasswordRecoveryJourney(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/portal.html", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `id="portal-reset-request"`) || !strings.Contains(page.Body.String(), `id="portal-reset-confirm"`) {
		t.Fatalf("portal recovery forms missing: status=%d body=%s", page.Code, page.Body)
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/portal-recovery.js", nil))
	if asset.Code != http.StatusOK || asset.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("recovery asset response: status=%d headers=%+v", asset.Code, asset.Header())
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
