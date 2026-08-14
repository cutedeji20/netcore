package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/security"
)

const sessionCookieName = "netcore_session"

type principalContextKey struct{}

// LoginLimiter is intentionally small so the authentication boundary can be
// tested without Redis and so the failure policy is visible to callers.
type LoginLimiter interface {
	AllowSlidingWindow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
}

// HTTP exposes the Phase 2 authentication endpoints.
type HTTP struct {
	service        *Service
	limiter        LoginLimiter
	secureCookies  bool
	allowedOrigins []string
}

func NewHTTP(service *Service, limiter LoginLimiter, secureCookies bool, allowedOrigins []string) (*HTTP, error) {
	if service == nil {
		return nil, errors.New("auth: service is required")
	}
	if limiter == nil {
		return nil, errors.New("auth: login limiter is required")
	}
	return &HTTP{
		service:        service,
		limiter:        limiter,
		secureCookies:  secureCookies,
		allowedOrigins: slices.Clone(allowedOrigins),
	}, nil
}

// Routes registers public authentication endpoints and the first protected
// identity endpoint. Future feature routes must be wrapped with RequireAuth
// and RequirePermission; permissions, never role names, authorize access.
func (h *HTTP) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/login", h.login)
	mux.HandleFunc("POST /auth/logout", h.logout)
	mux.Handle("GET /api/v1/me", h.RequireAuth(http.HandlerFunc(h.me)))
}

func (h *HTTP) login(w http.ResponseWriter, r *http.Request) {
	// A browser-originated cross-site login could set a session for an attacker
	// controlled account (login CSRF). Non-browser clients have no Origin and
	// remain supported; browsers must originate from an approved frontend.
	if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(h.allowedOrigins, origin) {
		security.WriteError(w, r, http.StatusForbidden, "CSRF_REJECTED", "Request origin is not allowed.")
		return
	}

	var input struct {
		Tenant     string `json:"tenant"`
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}

	identifier := NormalizeLoginIdentifier(input.Identifier)
	if err := h.limitLogin(r.Context(), input.Tenant, identifier, clientIP(r)); err != nil {
		if errors.Is(err, errRateLimited) {
			security.WriteError(w, r, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Too many attempts. Please try again later.")
			return
		}
		slog.Error("authentication rate limiter unavailable", slog.String("error", err.Error()))
		security.WriteError(w, r, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "Authentication is temporarily unavailable.")
		return
	}

	session, principal, err := h.service.Login(r.Context(), LoginInput{
		TenantSlug: input.Tenant,
		Identifier: identifier,
		Password:   input.Password,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
	})
	if errors.Is(err, ErrInvalidCredentials) {
		security.WriteError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials.")
		return
	}
	if err != nil {
		slog.Error("login failed", slog.String("error", err.Error()))
		security.WriteError(w, r, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "Authentication is temporarily unavailable.")
		return
	}

	h.setSessionCookie(w, session)
	writeJSON(w, http.StatusOK, meResponse{User: responseUser(principal), ExpiresAt: session.ExpiresAt})
}

func (h *HTTP) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if !slices.Contains(h.allowedOrigins, r.Header.Get("Origin")) {
			security.WriteError(w, r, http.StatusForbidden, "CSRF_REJECTED", "Request origin is not allowed.")
			return
		}
		if err := h.service.Logout(r.Context(), cookie.Value); err != nil {
			slog.Error("logout failed", slog.String("error", err.Error()))
			security.WriteError(w, r, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "Authentication is temporarily unavailable.")
			return
		}
	}
	h.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) me(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	writeJSON(w, http.StatusOK, meResponse{User: responseUser(principal)})
}

// RequireAuth loads the session principal and places it on the request context.
func (h *HTTP) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
			return
		}
		principal, err := h.service.Authenticate(r.Context(), cookie.Value)
		if errors.Is(err, ErrUnauthenticated) {
			security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
			return
		}
		if err != nil {
			slog.Error("session authentication failed", slog.String("error", err.Error()))
			security.WriteError(w, r, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "Authentication is temporarily unavailable.")
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePermission enforces authorization by explicit permission. A same-
// tenant permission failure is a 403; object routes use an additional tenant
// predicate and return 404 for cross-tenant objects.
func RequirePermission(permission string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok {
			security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
			return
		}
		if !principal.HasPermission(permission) {
			security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to perform this action.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PrincipalFromContext returns the authenticated principal installed by
// RequireAuth.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}

// ContextWithPrincipal installs an already-authenticated principal for trusted
// in-process callers and focused handler tests. It does not parse a credential
// or grant a permission; internet-facing routes must continue to use
// RequireAuth followed by RequirePermission.
func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

var errRateLimited = errors.New("auth: rate limited")

func (h *HTTP) limitLogin(ctx context.Context, tenant, identifier, ip string) error {
	accountKey := hashedRateLimitKey("auth:login:account", strings.ToLower(strings.TrimSpace(tenant)), identifier)
	allowed, err := h.limiter.AllowSlidingWindow(ctx, accountKey, 5, time.Minute)
	if err != nil {
		return err
	}
	if !allowed {
		return errRateLimited
	}

	ipKey := hashedRateLimitKey("auth:login:ip", ip)
	allowed, err = h.limiter.AllowSlidingWindow(ctx, ipKey, 20, time.Minute)
	if err != nil {
		return err
	}
	if !allowed {
		return errRateLimited
	}
	return nil
}

func hashedRateLimitKey(namespace string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return namespace + ":" + hex.EncodeToString(sum[:])
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	if net.ParseIP(host) == nil {
		return ""
	}
	return host
}

func (h *HTTP) setSessionCookie(w http.ResponseWriter, session Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		MaxAge:   int(time.Until(session.ExpiresAt).Seconds()),
		Secure:   h.secureCookies,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *HTTP) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   h.secureCookies,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

type meResponse struct {
	User      userResponse `json:"user"`
	ExpiresAt time.Time    `json:"expires_at,omitempty"`
}

type userResponse struct {
	ID          string   `json:"id"`
	TenantID    string   `json:"tenant_id"`
	Email       string   `json:"email,omitempty"`
	Permissions []string `json:"permissions"`
}

func responseUser(principal Principal) userResponse {
	permissions := make([]string, 0, len(principal.Permissions))
	for permission := range principal.Permissions {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return userResponse{
		ID:          principal.UserID,
		TenantID:    principal.TenantID,
		Email:       principal.Email,
		Permissions: permissions,
	}
}

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("additional JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
