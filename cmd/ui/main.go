// Command ui serves the NetCore control dashboard.
//
// It is deliberately locked until a same-origin production API is available;
// it never uses the sample-data fallback as an access-control mechanism.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
)

//go:embed assets
var assets embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:3000", "HTTP listen address")
	flag.Parse()

	handler, err := newHandler()
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *addr, Handler: handler}
	log.Printf("NetCore UI preview available at http://%s", *addr)
	log.Fatal(server.ListenAndServe())
}

func newHandler() (http.Handler, error) {
	adminConfig, err := loadAdminConfig(os.Getenv)
	if err != nil {
		return nil, err
	}
	portalConfig, err := loadPortalConfig(os.Getenv)
	if err != nil {
		return nil, err
	}
	content, err := fs.Sub(assets, "assets")
	if err != nil {
		return nil, fmt.Errorf("load UI assets: %w", err)
	}
	files := http.FileServer(http.FS(content))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; connect-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:")
		if r.URL.Path == "/admin-config.js" {
			serveAdminConfig(w, adminConfig)
			return
		}
		if r.URL.Path == "/portal-config.js" {
			servePortalConfig(w, portalConfig)
			return
		}
		// The UI uses stable asset names. Revalidate the HTML, styles and scripts
		// on every visit so a browser cannot combine a fresh deployment with an
		// older command-palette script from its cache.
		if r.URL.Path == "/" || r.URL.Path == "/index.html" || strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if r.URL.Path == "/portal.html" || r.URL.Path == "/staff-invite.html" {
			// A captive-portal handoff may briefly appear in the subsequent
			// RouterOS login URL. Invitation tokens live only in a URL fragment;
			// both sensitive pages must not be cached or forwarded as a Referer.
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; connect-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:")
		}
		files.ServeHTTP(w, r)
	}), nil
}

// adminConfig is deliberately public configuration. It contains no credential
// or API address because the dashboard is only allowed to use its own origin.
// The reverse proxy provides that origin and routes /api and /auth privately.
type adminConfig struct {
	Mode   string `json:"mode"`
	Tenant string `json:"tenant"`
}

// portalConfig contains only browser-safe same-origin configuration. The API
// itself resolves the tenant from deployment configuration, so the portal
// neither receives nor sends a tenant selector.
type portalConfig struct {
	Mode            string `json:"mode"`
	APIBase         string `json:"apiBase"`
	AccountsEnabled bool   `json:"accountsEnabled"`
	PaymentsEnabled bool   `json:"paymentsEnabled"`
}

func loadAdminConfig(getenv func(string) string) (adminConfig, error) {
	mode := strings.ToLower(strings.TrimSpace(getenv("NETCORE_UI_MODE")))
	if mode == "" || mode == "locked" {
		return adminConfig{Mode: "locked"}, nil
	}
	if mode != "live" {
		return adminConfig{}, fmt.Errorf("NETCORE_UI_MODE must be locked or live")
	}
	tenant := strings.ToLower(strings.TrimSpace(getenv("NETCORE_TENANT_SLUG")))
	if !validTenantSlug(tenant) {
		return adminConfig{}, fmt.Errorf("NETCORE_TENANT_SLUG must be a lowercase tenant slug when NETCORE_UI_MODE=live")
	}
	return adminConfig{Mode: "live", Tenant: tenant}, nil
}

func validTenantSlug(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func loadPortalConfig(getenv func(string) string) (portalConfig, error) {
	admin, err := loadAdminConfig(getenv)
	if err != nil {
		return portalConfig{}, err
	}
	if admin.Mode != "live" {
		return portalConfig{Mode: "preview"}, nil
	}
	return portalConfig{
		Mode: "live",
		// Provider activation is checked by the API on each operation from the
		// tenant's encrypted dashboard settings. Static provider flags would
		// require a UI redeploy after every safe admin configuration change.
		AccountsEnabled: true,
		PaymentsEnabled: true,
	}, nil
}

func serveAdminConfig(w http.ResponseWriter, config adminConfig) {
	payload, err := json.Marshal(config)
	if err != nil {
		http.Error(w, "admin configuration unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = fmt.Fprintf(w, "window.NETCORE_ADMIN_CONFIG = Object.freeze(%s);\n", payload)
}

func servePortalConfig(w http.ResponseWriter, config portalConfig) {
	payload, err := json.Marshal(config)
	if err != nil {
		http.Error(w, "portal configuration unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = fmt.Fprintf(w, "window.NETCORE_PORTAL_CONFIG = Object.freeze(%s);\n", payload)
}
