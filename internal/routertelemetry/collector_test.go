package routertelemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTargetRequiresPrivateHTTPSOrigin(t *testing.T) {
	valid := Target{RouterID: "22222222-2222-4222-8222-222222222222", URL: "https://172.16.0.4", WANInterface: "ether1", Username: "telemetry", PasswordFile: "/run/netcore/runtime/router-password"}
	if err := validateTarget(valid); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"http://172.16.0.4", "https://example.com", "https://8.8.8.8", "https://172.16.0.4/other", "https://admin:secret@172.16.0.4"} {
		invalid := valid
		invalid.URL = address
		if err := validateTarget(invalid); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
}

func TestManagementIPMatchesURLHostForPostgresINETText(t *testing.T) {
	for _, value := range []string{"192.168.88.1", "192.168.88.1/32"} {
		if !managementIPMatchesURL(value, "192.168.88.1") {
			t.Fatalf("management IP %q did not match its URL host", value)
		}
	}
	if managementIPMatchesURL("192.168.88.1/32", "192.168.88.2") {
		t.Fatal("different management IP matched URL host")
	}
}

func TestReadListUsesReadOnlyRouterPrint(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/interface/print" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "telemetry" || password != "secret" {
			t.Error("missing telemetry credentials")
		}
		var body map[string][]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body[".proplist"]) != 3 {
			t.Errorf("properties=%v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"ether1","rx-byte":"123","tx-byte":"456"}]`))
	}))
	defer server.Close()
	var interfaces []map[string]string
	err := readList(context.Background(), server.Client(), Target{URL: server.URL, Username: "telemetry"}, []byte("secret"), "/rest/interface/print", []string{"name", "rx-byte", "tx-byte"}, &interfaces)
	if err != nil || len(interfaces) != 1 || interfaces[0]["rx-byte"] != "123" {
		t.Fatalf("interfaces=%v err=%v", interfaces, err)
	}
}
