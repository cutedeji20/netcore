package auth

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/security"
)

var errAccountRateLimited = errors.New("auth: account rate limited")

// AccountHTTP exposes public, tenant-bound customer account routes. The
// tenant comes from reviewed deployment configuration rather than a query
// parameter or JSON field, so a visitor cannot select another tenant.
type AccountHTTP struct {
	service        *AccountService
	loginService   *Service
	limiter        LoginLimiter
	tenantSlug     string
	secureCookies  bool
	allowedOrigins []string
	trustedProxies []netip.Prefix
}

func NewAccountHTTP(service *AccountService, loginService *Service, limiter LoginLimiter, tenantSlug string, secureCookies bool, allowedOrigins, trustedProxies []string) (*AccountHTTP, error) {
	if service == nil {
		return nil, errors.New("auth: account service is required")
	}
	if loginService == nil {
		return nil, errors.New("auth: portal login service is required")
	}
	if limiter == nil {
		return nil, errors.New("auth: account rate limiter is required")
	}
	tenantSlug = strings.ToLower(strings.TrimSpace(tenantSlug))
	if tenantSlug == "" {
		return nil, errors.New("auth: portal tenant slug is required")
	}
	proxies, err := parseTrustedProxies(trustedProxies)
	if err != nil {
		return nil, err
	}
	return &AccountHTTP{
		service: service, loginService: loginService, limiter: limiter, tenantSlug: tenantSlug, secureCookies: secureCookies,
		allowedOrigins: slices.Clone(allowedOrigins), trustedProxies: proxies,
	}, nil
}

func (h *AccountHTTP) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /portal/auth/login", h.portalLogin)
	mux.HandleFunc("POST /portal/auth/register", h.register)
	mux.HandleFunc("POST /portal/auth/verify-email", h.verifyEmail)
	mux.HandleFunc("POST /portal/auth/password-reset/request", h.requestPasswordReset)
	mux.HandleFunc("POST /portal/auth/password-reset/confirm", h.confirmPasswordReset)
}

func (h *AccountHTTP) portalLogin(w http.ResponseWriter, r *http.Request) {
	if !h.allowsOrigin(w, r) {
		return
	}
	var input struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	if err := h.limit(r.Context(), "portal-login", input.Identifier, h.clientIP(r)); err != nil {
		h.writeRateLimitError(w, r, err)
		return
	}
	session, _, err := h.loginService.Login(r.Context(), LoginInput{
		TenantSlug: h.tenantSlug,
		Identifier: input.Identifier,
		Password:   input.Password,
		IP:         h.clientIP(r),
		UserAgent:  r.UserAgent(),
	})
	if errors.Is(err, ErrInvalidCredentials) {
		security.WriteError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "Authentication is temporarily unavailable.")
		return
	}
	h.setSessionCookie(w, session)
	writeJSON(w, http.StatusOK, accountLoginResponse{ExpiresAt: session.ExpiresAt})
}

func (h *AccountHTTP) register(w http.ResponseWriter, r *http.Request) {
	if !h.allowsOrigin(w, r) {
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	if err := h.limit(r.Context(), "registration", input.Email, h.clientIP(r)); err != nil {
		h.writeRateLimitError(w, r, err)
		return
	}
	issued, err := h.service.BeginRegistration(r.Context(), RegistrationInput{TenantSlug: h.tenantSlug, Email: input.Email, Password: input.Password})
	if err != nil {
		h.writeAccountError(w, r, err, "We could not send a verification code. Please try again shortly.")
		return
	}
	writeJSON(w, http.StatusAccepted, accountCodeResponse{ChallengeID: issued.ChallengeID, ExpiresAt: issued.ExpiresAt})
}

func (h *AccountHTTP) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if !h.allowsOrigin(w, r) {
		return
	}
	var input struct {
		Email       string `json:"email"`
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
	}
	if err := decodeJSON(r, &input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	if err := h.limit(r.Context(), "verification", input.Email, h.clientIP(r)); err != nil {
		h.writeRateLimitError(w, r, err)
		return
	}
	err := h.service.VerifyRegistration(r.Context(), EmailVerificationInput{
		TenantSlug: h.tenantSlug, Email: input.Email, ChallengeID: input.ChallengeID, Code: input.Code,
	})
	if errors.Is(err, ErrInvalidOTP) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_VERIFICATION_CODE", "The verification code is invalid or has expired.")
		return
	}
	if err != nil {
		h.writeAccountError(w, r, err, "We could not verify that code. Please try again shortly.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AccountHTTP) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	if !h.allowsOrigin(w, r) {
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(r, &input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	if err := h.limit(r.Context(), "password-reset", input.Email, h.clientIP(r)); err != nil {
		h.writeRateLimitError(w, r, err)
		return
	}
	issued, err := h.service.RequestPasswordReset(r.Context(), h.tenantSlug, input.Email)
	if err != nil {
		h.writeAccountError(w, r, err, "We could not send a reset code. Please try again shortly.")
		return
	}
	writeJSON(w, http.StatusAccepted, accountCodeResponse{ChallengeID: issued.ChallengeID, ExpiresAt: issued.ExpiresAt})
}

func (h *AccountHTTP) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	if !h.allowsOrigin(w, r) {
		return
	}
	var input struct {
		Email       string `json:"email"`
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
		Password    string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	if err := h.limit(r.Context(), "password-reset-confirm", input.Email, h.clientIP(r)); err != nil {
		h.writeRateLimitError(w, r, err)
		return
	}
	err := h.service.ConfirmPasswordReset(r.Context(), PasswordResetInput{
		TenantSlug: h.tenantSlug, Email: input.Email, ChallengeID: input.ChallengeID, Code: input.Code, Password: input.Password,
	})
	if errors.Is(err, ErrInvalidOTP) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_RESET_CODE", "The reset code is invalid or has expired.")
		return
	}
	if err != nil {
		h.writeAccountError(w, r, err, "We could not reset this password. Please try again shortly.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AccountHTTP) allowsOrigin(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(h.allowedOrigins, origin) {
		security.WriteError(w, r, http.StatusForbidden, "CSRF_REJECTED", "Request origin is not allowed.")
		return false
	}
	return true
}

func (h *AccountHTTP) limit(ctx context.Context, action, email, ip string) error {
	accountKey := hashedRateLimitKey("auth:"+action+":account", h.tenantSlug, strings.ToLower(strings.TrimSpace(email)))
	allowed, err := h.limiter.AllowSlidingWindow(ctx, accountKey, 3, 15*time.Minute)
	if err != nil {
		return err
	}
	if !allowed {
		return errAccountRateLimited
	}
	ipKey := hashedRateLimitKey("auth:"+action+":ip", ip)
	allowed, err = h.limiter.AllowSlidingWindow(ctx, ipKey, 10, time.Hour)
	if err != nil {
		return err
	}
	if !allowed {
		return errAccountRateLimited
	}
	return nil
}

func (h *AccountHTTP) writeRateLimitError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errAccountRateLimited) {
		security.WriteError(w, r, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Please wait a moment and try again.")
		return
	}
	security.WriteError(w, r, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "Account service is temporarily unavailable.")
}

func (h *AccountHTTP) writeAccountError(w http.ResponseWriter, r *http.Request, err error, unavailableMessage string) {
	if errors.Is(err, ErrInvalidAccountInput) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	security.WriteError(w, r, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", unavailableMessage)
}

func (h *AccountHTTP) clientIP(r *http.Request) string {
	return clientAddress(r, h.trustedProxies)
}

func (h *AccountHTTP) setSessionCookie(w http.ResponseWriter, session Session) {
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

type accountCodeResponse struct {
	ChallengeID string    `json:"challenge_id"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type accountLoginResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
}
