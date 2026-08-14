package security

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// §45 — a correlation ID is always present and always echoed.
func TestRequestID_GeneratedWhenAbsent(t *testing.T) {
	rec := httptest.NewRecorder()
	RequestID(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	id := rec.Header().Get(HeaderRequestID)
	if id == "" {
		t.Fatal("no request ID generated")
	}
	if len(id) != 32 {
		t.Errorf("id %q has length %d, want 32 hex chars", id, len(id))
	}
}

func TestRequestID_SafeClientValueIsHonoured(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderRequestID, "trace-abc_123")
	rec := httptest.NewRecorder()

	RequestID(okHandler()).ServeHTTP(rec, req)
	if got := rec.Header().Get(HeaderRequestID); got != "trace-abc_123" {
		t.Errorf("id = %q, want the client value", got)
	}
}

// An unvalidated client header propagated into JSON logs is a log-injection
// vector. Each of these must be replaced, not echoed.
func TestRequestID_HostileClientValueIsReplaced(t *testing.T) {
	hostile := []string{
		`abc","level":"ERROR","msg":"fake`, // JSON log injection
		"abc\ninjected-line",               // newline injection
		"abc\r\nSet-Cookie: x=y",           // header injection
		strings.Repeat("a", 500),           // unbounded length
		"",                                 // empty
		"../../etc/passwd",                 // path chars
		"abc;DROP TABLE x",                 // punctuation
	}
	for _, h := range hostile {
		t.Run(strings.ReplaceAll(h[:min(len(h), 12)], "\n", "\\n"), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(HeaderRequestID, h)
			rec := httptest.NewRecorder()

			RequestID(okHandler()).ServeHTTP(rec, req)

			got := rec.Header().Get(HeaderRequestID)
			if got == h {
				t.Fatalf("hostile request ID %q was echoed verbatim", h)
			}
			if !validRequestID(got) {
				t.Fatalf("replacement %q is itself invalid", got)
			}
		})
	}
}

// §56 — the header set.
func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(DefaultAPIHeaders())(okHandler()).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'; base-uri 'none'",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if rec.Header().Get("Permissions-Policy") == "" {
		t.Error("Permissions-Policy missing")
	}
}

// HSTS on a plaintext dev listener pins localhost to https in the developer's
// browser, which is a genuinely annoying self-inflicted outage.
func TestSecurityHeaders_HSTSOnlyOverTLS(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(DefaultAPIHeaders())(okHandler()).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS sent over plaintext: %q", got)
	}
}

// §57 — default deny.
func TestCORS(t *testing.T) {
	allowed := []string{"https://portal.example.com"}

	t.Run("allowed origin is echoed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://portal.example.com")
		rec := httptest.NewRecorder()
		CORS(allowed)(okHandler()).ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://portal.example.com" {
			t.Errorf("ACAO = %q", got)
		}
		if got := rec.Header().Get("Vary"); !strings.Contains(got, "Origin") {
			t.Errorf("Vary = %q, must include Origin", got)
		}
	})

	t.Run("unknown origin gets no CORS headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		rec := httptest.NewRecorder()
		CORS(allowed)(okHandler()).ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("ACAO = %q, want empty for a non-allowlisted origin", got)
		}
	})

	// §1.19 — there must be no code path that emits a wildcard.
	t.Run("wildcard is never emitted even if configured", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://anything.example.com")
		rec := httptest.NewRecorder()
		CORS([]string{"*"})(okHandler()).ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
			t.Fatal("wildcard ACAO emitted (§1.19)")
		}
	})

	t.Run("preflight short-circuits", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", "https://portal.example.com")
		rec := httptest.NewRecorder()
		CORS(allowed)(okHandler()).ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("preflight status = %d, want 204", rec.Code)
		}
	})
}

// §16 — oversized bodies are rejected with 413, in the standard envelope.
func TestBodyLimit(t *testing.T) {
	t.Run("declared oversize is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 200)))
		req.ContentLength = 200
		rec := httptest.NewRecorder()
		BodyLimit(100)(okHandler()).ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
		var body ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("error body is not the standard envelope: %v", err)
		}
		if body.Error.Code != "REQUEST_TOO_LARGE" {
			t.Errorf("code = %q", body.Error.Code)
		}
	})

	t.Run("within limit passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("small"))
		rec := httptest.NewRecorder()
		BodyLimit(100)(okHandler()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

// §49 — the handler must observe a bounded context.
func TestTimeout_SetsDeadline(t *testing.T) {
	var hadDeadline bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadDeadline = r.Context().Deadline()
	})
	Timeout(50*time.Millisecond)(h).ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil))

	if !hadDeadline {
		t.Fatal("handler context had no deadline (§49)")
	}
}

// §95/§96 — error envelope and the 404-not-403 rule.
func TestWriteError_Envelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusConflict, "SUBSCRIPTION_NOT_ACTIVE", "Subscription is not active.")
	}), RequestID).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "SUBSCRIPTION_NOT_ACTIVE" {
		t.Errorf("code = %q", body.Error.Code)
	}
	if body.Error.RequestID == "" {
		t.Error("request_id must be present for support correlation (§95)")
	}
}

func TestWriteNotFound_Returns404Not403(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	WriteNotFound(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — a 403 confirms the resource exists "+
			"and is an enumeration oracle (§96)", rec.Code)
	}
}

// Chain must apply middleware outermost-first.
func TestChain_Order(t *testing.T) {
	var order []string
	mk := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})
	Chain(final, mk("a"), mk("b"), mk("c")).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"a", "b", "c", "handler"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// §26 — MAC normalization across every vendor format.
func TestNormalizeMAC(t *testing.T) {
	valid := map[string]string{
		"AA:BB:CC:DD:EE:FF": "aabbccddeeff",
		"aa-bb-cc-dd-ee-ff": "aabbccddeeff",
		"aabb.ccdd.eeff":    "aabbccddeeff",
		"AABBCCDDEEFF":      "aabbccddeeff",
		"aa bb cc dd ee ff": "aabbccddeeff",
		"00:00:00:00:00:00": "000000000000",
	}
	for in, want := range valid {
		got, ok := NormalizeMAC(in)
		if !ok {
			t.Errorf("NormalizeMAC(%q) rejected a valid address", in)
			continue
		}
		if got != want {
			t.Errorf("NormalizeMAC(%q) = %q, want %q", in, got, want)
		}
	}

	invalid := []string{
		"", "aabbccddee", "aabbccddeeffaa", "zz:bb:cc:dd:ee:ff",
		"aabbccddeeg f", "'; DROP TABLE devices--",
	}
	for _, in := range invalid {
		if got, ok := NormalizeMAC(in); ok {
			t.Errorf("NormalizeMAC(%q) = %q, want rejection", in, got)
		}
	}
}

// All formats of one address must collapse to the same key, or device limits
// and session lookups silently break.
func TestNormalizeMAC_FormatsAreEquivalent(t *testing.T) {
	forms := []string{"AA:BB:CC:DD:EE:FF", "aa-bb-cc-dd-ee-ff", "aabb.ccdd.eeff", "AaBbCcDdEeFf"}
	var first string
	for i, f := range forms {
		got, ok := NormalizeMAC(f)
		if !ok {
			t.Fatalf("%q rejected", f)
		}
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("%q normalized to %q, but %q gave %q", f, got, forms[0], first)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
