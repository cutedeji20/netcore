// Package security provides HTTP middleware.
//
// Spec: BUILD.md §14, §16, §45, §49, §56, §57, §95, §96.
//
// Middleware order matters and is asserted by Chain's documentation:
//
//	RequestID -> SecurityHeaders -> CORS -> BodyLimit -> Timeout -> handler
//
// RequestID first so every later log line and error carries it; Timeout last
// so it wraps only the handler's own work.
package security

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/logger"
)

// ---------------------------------------------------------------------------
// §45 — request correlation
// ---------------------------------------------------------------------------

// HeaderRequestID is the correlation header.
const HeaderRequestID = "X-Request-ID"

// RequestID assigns a correlation ID to every request.
//
// A client-supplied value is accepted only if it is safe: bounded length and
// a restricted alphabet. An unvalidated client header propagated into logs is
// a log-injection vector (§45).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(HeaderRequestID, id)
		ctx := logger.WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validRequestID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is not recoverable and must not silently
		// degrade to a predictable ID.
		panic("security: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// ---------------------------------------------------------------------------
// §56 — security headers
// ---------------------------------------------------------------------------

// HeaderPolicy configures security headers. The captive portal and the admin
// panel need different CSPs (§56): the portal loads gateway scripts, the admin
// panel must not. Two policies, both explicit.
type HeaderPolicy struct {
	ContentSecurityPolicy string
	HSTSMaxAge            time.Duration
	EnableHSTS            bool
	ReferrerPolicy        string
	PermissionsPolicy     string
	FrameOptions          string
}

// DefaultAPIHeaders returns the policy for JSON APIs, which load no resources
// at all and can therefore use the strictest possible CSP.
func DefaultAPIHeaders() HeaderPolicy {
	return HeaderPolicy{
		ContentSecurityPolicy: "default-src 'none'; frame-ancestors 'none'; base-uri 'none'",
		HSTSMaxAge:            365 * 24 * time.Hour,
		EnableHSTS:            true,
		ReferrerPolicy:        "no-referrer",
		PermissionsPolicy:     "geolocation=(), microphone=(), camera=(), payment=()",
		FrameOptions:          "DENY",
	}
}

// SecurityHeaders applies p to every response.
func SecurityHeaders(p HeaderPolicy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			if p.ContentSecurityPolicy != "" {
				h.Set("Content-Security-Policy", p.ContentSecurityPolicy)
			}
			if p.ReferrerPolicy != "" {
				h.Set("Referrer-Policy", p.ReferrerPolicy)
			}
			if p.PermissionsPolicy != "" {
				h.Set("Permissions-Policy", p.PermissionsPolicy)
			}
			if p.FrameOptions != "" {
				h.Set("X-Frame-Options", p.FrameOptions)
			}
			// HSTS only over TLS: sending it on a plaintext dev listener
			// pins localhost to https in the developer's browser.
			if p.EnableHSTS && r.TLS != nil {
				h.Set("Strict-Transport-Security",
					"max-age="+itoa(int64(p.HSTSMaxAge.Seconds()))+"; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// §57 — CORS, default deny
// ---------------------------------------------------------------------------

// CORS allows only explicitly listed origins. There is no wildcard path
// through this function: §1.19 forbids `*` for authenticated APIs, and
// config.Validate rejects it in production before the process starts.
func CORS(allowed []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && slices.Contains(allowed, origin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Headers",
					"Content-Type, Authorization, "+HeaderRequestID+", Idempotency-Key")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Max-Age", "600")
				// Responses vary by Origin; without this a shared cache can
				// serve one tenant's CORS headers to another origin.
				h.Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// §16 — resource exhaustion
// ---------------------------------------------------------------------------

// BodyLimit caps request body size. Exceeding it yields 413, not a 500 from a
// downstream decoder that ran out of memory.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				WriteError(w, r, http.StatusRequestEntityTooLarge,
					"REQUEST_TOO_LARGE", "Request body exceeds the permitted size.")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout bounds handler execution (§49).
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---------------------------------------------------------------------------
// §95 — error format
// ---------------------------------------------------------------------------

// ErrorBody is the standard error envelope. Clients branch on Code, never on
// Message — message text is for humans and will be translated.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries a stable code, a human message, and the correlation ID.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteError emits the standard envelope.
//
// It deliberately takes a caller-supplied message rather than an error value:
// §95 forbids leaking stack traces, SQL errors, and internal paths to clients.
// Log the underlying error separately, keyed by the same request ID.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorBody{Error: ErrorDetail{
		Code:      code,
		Message:   message,
		RequestID: logger.RequestID(r.Context()),
	}})
}

// WriteNotFound is the response for a resource that either does not exist or
// belongs to another tenant.
//
// §96: cross-tenant access returns 404, never 403. A 403 confirms the
// resource exists, which is an enumeration oracle (§76).
func WriteNotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Resource not found.")
}

// ---------------------------------------------------------------------------
// Composition
// ---------------------------------------------------------------------------

// Middleware is the standard decorator signature.
type Middleware func(http.Handler) http.Handler

// Chain composes middleware so that Chain(h, a, b, c) executes a, then b,
// then c, then h.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// Standard returns the default middleware stack in the required order.
func Standard(origins []string, maxBody int64, timeout time.Duration) []Middleware {
	return []Middleware{
		RequestID,
		SecurityHeaders(DefaultAPIHeaders()),
		CORS(origins),
		BodyLimit(maxBody),
		Timeout(timeout),
	}
}

// itoa avoids importing strconv for one call site.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// NormalizeMAC canonicalizes a MAC address to lowercase hex with no
// separators (§26).
//
// Accepts AA:BB:CC:DD:EE:FF, aa-bb-cc-dd-ee-ff, aabb.ccdd.eeff and bare hex.
// Returns ok=false for anything else — MAC is an identifier, and a malformed
// one must never be silently coerced into a lookup key.
func NormalizeMAC(s string) (string, bool) {
	var b strings.Builder
	b.Grow(12)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			b.WriteByte(c)
		case c >= 'A' && c <= 'F':
			b.WriteByte(c + ('a' - 'A'))
		case c == ':' || c == '-' || c == '.' || c == ' ':
			// separator, skip
		default:
			return "", false
		}
	}
	out := b.String()
	if len(out) != 12 {
		return "", false
	}
	return out, true
}
