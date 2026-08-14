package portal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortalDeploymentTemplatesPreserveHandoffBoundaries(t *testing.T) {
	read := func(parts ...string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	routerOS := read("routeros", "hotspot", "provision-hotspot.rsc.tmpl")
	if !strings.Contains(routerOS, "login-by=http-pap,mac-cookie") || !strings.Contains(routerOS, "radius-accounting=yes") || !strings.Contains(routerOS, "radius-interim-update=received") || !strings.Contains(routerOS, "html-directory-override=$hotspotHtmlDir") || !strings.Contains(routerOS, "/ip hotspot user profile set") || !strings.Contains(routerOS, "__RADIUS_SHARED_SECRET__") || !strings.Contains(routerOS, "__HOTSPOT_HTML_DIR__") || !strings.Contains(routerOS, "__COA_SOURCE_ADDRESS__") || !strings.Contains(routerOS, "/radius incoming set accept=yes port=3799") || !strings.Contains(routerOS, "netcore-coa-drop") {
		t.Fatal("RouterOS template lost the PAP handoff, accounting, CoA restriction, local HTML, or secret placeholder")
	}

	login := read("routeros", "hotspot", "login.html.tmpl")
	for _, want := range []string{"client_mac", "nas_address", "hotspot_login_url", "new URL(\"http://$(server-address)\")", "hotspotServer.hostname", "__PORTAL_ORIGIN__"} {
		if !strings.Contains(login, want) {
			t.Fatalf("RouterOS login template missing %q", want)
		}
	}
	if strings.Contains(login, "tenant=") {
		t.Fatal("RouterOS login template must not accept a tenant from the browser")
	}
	for _, template := range []string{"flogin.html.tmpl", "error.html.tmpl", "maintenance.html.tmpl"} {
		body := read("routeros", "hotspot", template)
		if !strings.Contains(body, "local router") && template == "maintenance.html.tmpl" {
			t.Fatalf("RouterOS maintenance template %q must state that it is local", template)
		}
		if strings.Contains(body, "__PORTAL_ORIGIN__") {
			t.Fatalf("RouterOS fallback template %q must remain locally served", template)
		}
	}

	policy := read("freeradius", "policy.d", "netcore_portal")
	if strings.Count(policy, "FROM radius_portal_handoff_authorize") != 1 || strings.Contains(policy, "FROM radius_portal_handoff_consume") {
		t.Fatal("RADIUS policy must use exactly one atomic handoff authorization query")
	}
	if !strings.Contains(policy, "Mikrotik-Total-Limit-Gigawords") || !strings.Contains(policy, "^[A-Za-z0-9_-]{43}$") {
		t.Fatal("RADIUS policy lost gigawords or nonce-shape enforcement")
	}

	accounting := read("freeradius", "policy.d", "netcore_accounting")
	if !strings.Contains(accounting, "radius_accounting_ingest") || !strings.Contains(accounting, "Event-Timestamp#") || !strings.Contains(accounting, "Acct-Input-Gigawords#") {
		t.Fatal("RADIUS accounting policy lost the narrow ingestion call or 64-bit counter inputs")
	}

	server := read("freeradius", "sites-enabled", "netcore-hotspot")
	if !strings.Contains(server, "acct_unique") || !strings.Contains(server, "netcore_accounting") {
		t.Fatal("HotSpot server template lost accounting id generation or ingestion")
	}
}
