package portal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

var errRateLimited = errors.New("portal: rate limited")

// HandoffLimiter uses short-lived Redis counters. Handoff issuance fails
// closed if these counters are unavailable, so a Redis outage cannot remove
// the anti-abuse boundary from a public captive portal.
type HandoffLimiter interface {
	AllowSlidingWindow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
}

// HTTP exposes the authenticated captive-portal handoff step.
type HTTP struct {
	service        *Service
	limiter        HandoffLimiter
	allowedOrigins []string
}

func NewHTTP(service *Service, limiter HandoffLimiter, allowedOrigins []string) (*HTTP, error) {
	if service == nil {
		return nil, errors.New("portal: service is required")
	}
	if limiter == nil {
		return nil, errors.New("portal: handoff limiter is required")
	}
	return &HTTP{
		service:        service,
		limiter:        limiter,
		allowedOrigins: slices.Clone(allowedOrigins),
	}, nil
}

// Routes intentionally provides no unauthenticated authorization endpoint.
// The browser authenticates through /auth/login first; a handoff is issued
// only for that authenticated customer and an active subscription.
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("portal: mux and session authentication are required")
	}
	mux.Handle(
		"POST /api/v1/portal/handoff",
		sessions.RequireAuth(http.HandlerFunc(h.issue)),
	)
	return nil
}

func (h *HTTP) issue(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(h.allowedOrigins, origin) {
		security.WriteError(w, r, http.StatusForbidden, "CSRF_REJECTED", "Request origin is not allowed.")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" || principal.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}

	var input struct {
		ClientMAC       string `json:"client_mac"`
		NASAddress      string `json:"nas_address"`
		HotspotLoginURL string `json:"hotspot_login_url"`
	}
	if err := decodeJSON(r, &input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Connection details are invalid.")
		return
	}
	normalizedMAC, validMAC := security.NormalizeMAC(input.ClientMAC)
	nas, nasErr := netip.ParseAddr(strings.TrimSpace(input.NASAddress))
	if !validMAC || nasErr != nil {
		security.WriteError(w, r, http.StatusBadRequest, "PORTAL_CONTEXT_INVALID", "Unable to continue this connection.")
		return
	}
	if err := h.limitIssue(r.Context(), principal.TenantID, principal.UserID, normalizedMAC, nas.String()); err != nil {
		if errors.Is(err, errRateLimited) {
			security.WriteError(w, r, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Please wait a moment and try again.")
			return
		}
		security.WriteError(w, r, http.StatusServiceUnavailable, "PORTAL_UNAVAILABLE", "We cannot continue sign-in right now. Please try again shortly.")
		return
	}

	handoff, err := h.service.Issue(r.Context(), principal.TenantID, principal.UserID, normalizedMAC, nas.String(), input.HotspotLoginURL)
	switch {
	case errors.Is(err, ErrNoActivePlan):
		security.WriteError(w, r, http.StatusConflict, "NO_ACTIVE_PLAN", "No active internet plan was found. Choose a plan or sign in with another account.")
	case errors.Is(err, ErrInvalidContext):
		security.WriteError(w, r, http.StatusBadRequest, "PORTAL_CONTEXT_INVALID", "Unable to continue this connection.")
	case err != nil:
		security.WriteError(w, r, http.StatusServiceUnavailable, "PORTAL_UNAVAILABLE", "We cannot continue sign-in right now. Please try again shortly.")
	default:
		writeJSON(w, http.StatusCreated, handoffResponse{RedirectURL: handoff.RedirectURL, ExpiresAt: handoff.ExpiresAt})
	}
}

func (h *HTTP) limitIssue(ctx context.Context, tenantID, userID, normalizedMAC, nasAddress string) error {
	accountKey := rateLimitKey("portal:handoff:account", tenantID, userID)
	allowed, err := h.limiter.AllowSlidingWindow(ctx, accountKey, 10, time.Minute)
	if err != nil {
		return err
	}
	if !allowed {
		return errRateLimited
	}
	deviceKey := rateLimitKey("portal:handoff:device", tenantID, nasAddress, normalizedMAC)
	allowed, err = h.limiter.AllowSlidingWindow(ctx, deviceKey, 10, time.Minute)
	if err != nil {
		return err
	}
	if !allowed {
		return errRateLimited
	}
	return nil
}

func rateLimitKey(namespace string, values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return namespace + ":" + hex.EncodeToString(sum[:])
}

// handoffResponse contains the one token that may appear in a URL: the
// short-lived RouterOS login handoff. It deliberately omits the subscription,
// account, MAC, NAS, and nonce digest.
type handoffResponse struct {
	RedirectURL string    `json:"redirect_url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func decodeJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
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
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
