package network

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

const maxSearchLength = 120

// HTTP exposes staff-only router inventory read endpoints.
type HTTP struct {
	store           Store
	lifecycle       *Service
	tethering       *TetheringService
	defaultPageSize int
	maxPageSize     int
	clientIP        func(*http.Request) string
}

// ConfigureLifecycle adds secret-bearing routes only after the API has a
// configured Key Vault wrapper and private RADIUS address.
func (h *HTTP) ConfigureLifecycle(service *Service)          { h.lifecycle = service }
func (h *HTTP) ConfigureTethering(service *TetheringService) { h.tethering = service }

func NewHTTP(store Store, defaultPageSize, maxPageSize int) (*HTTP, error) {
	if store == nil {
		return nil, errors.New("network: store is required")
	}
	if defaultPageSize < 1 || maxPageSize < defaultPageSize {
		return nil, errors.New("network: invalid page size configuration")
	}
	return &HTTP{
		store:           store,
		defaultPageSize: defaultPageSize,
		maxPageSize:     maxPageSize,
	}, nil
}

// Routes separates inventory reading from router mutations and CoA controls,
// which will require network.write plus audit and queue-backed enforcement.
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("network: mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/network/routers",
		sessions.RequireAuth(auth.RequirePermission("network.read", http.HandlerFunc(h.list))),
	)
	mux.Handle("POST /api/v1/network/routers", sessions.RequireAuth(auth.RequirePermission("network.write", http.HandlerFunc(h.createRouter))))
	mux.Handle("POST /api/v1/network/routers/{routerID}/aaa/export", sessions.RequireAuth(auth.RequirePermission("network.write", http.HandlerFunc(h.export))))
	mux.Handle("GET /api/v1/network/routers/{routerID}/aaa", sessions.RequireAuth(auth.RequirePermission("network.read", http.HandlerFunc(h.aaa))))
	mux.Handle("POST /api/v1/network/routers/{routerID}/aaa/verify", sessions.RequireAuth(auth.RequirePermission("network.write", http.HandlerFunc(h.verifyAAA))))
	mux.Handle("POST /api/v1/network/routers/{routerID}/aaa/activate", sessions.RequireAuth(auth.RequirePermission("network.write", h.changeNASStatus(NASStatusActive))))
	mux.Handle("POST /api/v1/network/routers/{routerID}/aaa/disable", sessions.RequireAuth(auth.RequirePermission("network.write", h.changeNASStatus(NASStatusDisabled))))
	mux.Handle("GET /api/v1/network/tethering-policy", sessions.RequireAuth(auth.RequirePermission("network.read", http.HandlerFunc(h.getTetheringPolicy))))
	mux.Handle("PUT /api/v1/network/tethering-policy", sessions.RequireAuth(auth.RequirePermission("network.write", http.HandlerFunc(h.putTetheringPolicy))))
	h.clientIP = sessions.ClientIP
	return nil
}

func (h *HTTP) getTetheringPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || p.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if h.tethering == nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_UNAVAILABLE", "Tethering policy is not configured.")
		return
	}
	policy, err := h.tethering.Get(r.Context(), p.TenantID)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_UNAVAILABLE", "Tethering policy is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data struct {
			Enabled           bool `json:"enabled"`
			ExpectedClientTTL int  `json:"expected_client_ttl"`
		} `json:"data"`
	}{Data: struct {
		Enabled           bool `json:"enabled"`
		ExpectedClientTTL int  `json:"expected_client_ttl"`
	}{policy.Enabled, policy.ExpectedClientTTL}})
}

func (h *HTTP) putTetheringPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || p.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !p.HasPermission("network.write") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to change tethering policy.")
		return
	}
	if h.tethering == nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_UNAVAILABLE", "Tethering policy is not configured.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	var in struct {
		Enabled           bool `json:"enabled"`
		ExpectedClientTTL int  `json:"expected_client_ttl"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil || d.Decode(&struct{}{}) != io.EOF || in.ExpectedClientTTL < 64 || in.ExpectedClientTTL > 255 {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Expected client TTL must be between 64 and 255.")
		return
	}
	if err := h.tethering.Update(r.Context(), p, h.mutationActor(r, p.UserID), TetheringPolicy{Enabled: in.Enabled, ExpectedClientTTL: in.ExpectedClientTTL}); err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_UNAVAILABLE", "Tethering policy could not be saved.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data struct {
			Enabled           bool `json:"enabled"`
			ExpectedClientTTL int  `json:"expected_client_ttl"`
		} `json:"data"`
	}{Data: struct {
		Enabled           bool `json:"enabled"`
		ExpectedClientTTL int  `json:"expected_client_ttl"`
	}{in.Enabled, in.ExpectedClientTTL}})
}

func (h *HTTP) mutationActor(r *http.Request, userID string) MutationActor {
	ip := ""
	if h.clientIP != nil {
		ip = h.clientIP(r)
	}
	return MutationActor{UserID: userID, IP: ip, UserAgent: r.UserAgent()}
}

func (h *HTTP) createRouter(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !principal.HasPermission("network.write") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to manage network AAA.")
		return
	}
	if h.lifecycle == nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is not configured.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	defer r.Body.Close()
	var input struct {
		Name           string `json:"name"`
		SiteID         string `json:"site_id"`
		ManagementIP   string `json:"management_ip"`
		NASIPAddress   string `json:"nas_ip_address"`
		RadiusSourceIP string `json:"radius_source_ip"`
		Password       string `json:"password"`
		MFACode        string `json:"mfa_code"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Router details and administrator confirmation are required.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	created, err := h.lifecycle.CreateRouter(r.Context(), principal, h.mutationActor(r, principal.UserID), input.Password, input.MFACode, RouterCreateInput{Name: input.Name, SiteID: input.SiteID, ManagementIP: input.ManagementIP, NASIPAddress: input.NASIPAddress, RadiusSourceIP: input.RadiusSourceIP})
	if errors.Is(err, ErrStepUpFailed) {
		security.WriteError(w, r, http.StatusUnauthorized, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
		return
	}
	if errors.Is(err, ErrInvalidRouterInput) || errors.Is(err, ErrServiceUnavailable) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Router details are invalid.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		RouterID string    `json:"router_id"`
		NASID    string    `json:"nas_id"`
		Status   NASStatus `json:"status"`
	}{created.RouterID, created.NASID, created.Status})
}

func (h *HTTP) changeNASStatus(status NASStatus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok || principal.TenantID == "" {
			security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
			return
		}
		if !principal.HasPermission("network.write") {
			security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to manage network AAA.")
			return
		}
		if h.lifecycle == nil {
			security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is not configured.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		defer r.Body.Close()
		var input struct {
			Password string `json:"password"`
			MFACode  string `json:"mfa_code"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Password and authenticator confirmation are required.")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
			return
		}
		if err := h.lifecycle.SetStatus(r.Context(), principal, h.mutationActor(r, principal.UserID), input.Password, input.MFACode, r.PathValue("routerID"), status); errors.Is(err, ErrStepUpFailed) {
			security.WriteError(w, r, http.StatusUnauthorized, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
			return
		} else if errors.Is(err, ErrNotFound) {
			security.WriteError(w, r, http.StatusNotFound, "ROUTER_NOT_FOUND", "Router AAA configuration was not found.")
			return
		} else if err != nil {
			security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is temporarily unavailable.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *HTTP) aaa(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !principal.HasPermission("network.read") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to view network AAA.")
		return
	}
	if h.lifecycle == nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is not configured.")
		return
	}
	configuration, err := h.lifecycle.AAA(r.Context(), principal.TenantID, r.PathValue("routerID"))
	if errors.Is(err, ErrNotFound) {
		security.WriteError(w, r, http.StatusNotFound, "ROUTER_NOT_FOUND", "Router AAA configuration was not found.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		RouterID       string    `json:"router_id"`
		NASID          string    `json:"nas_id"`
		NASIPAddress   string    `json:"nas_ip_address"`
		RadiusSourceIP string    `json:"radius_source_ip"`
		ShortName      string    `json:"short_name"`
		Status         NASStatus `json:"status"`
		Version        int64     `json:"version"`
		Verified       bool      `json:"verified"`
	}{configuration.RouterID, configuration.NASID, configuration.NASIPAddress, configuration.RadiusSourceIP, configuration.ShortName, configuration.Status, configuration.Version, configuration.Verified})
}

func (h *HTTP) verifyAAA(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !principal.HasPermission("network.write") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to manage network AAA.")
		return
	}
	if h.lifecycle == nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is not configured.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	var input struct {
		Password  string `json:"password"`
		MFACode   string `json:"mfa_code"`
		Confirmed bool   `json:"private_test_confirmed"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Private test confirmation, password and authenticator code are required.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	if err := h.lifecycle.VerifyAAA(r.Context(), principal, h.mutationActor(r, principal.UserID), input.Password, input.MFACode, r.PathValue("routerID"), input.Confirmed); errors.Is(err, ErrStepUpFailed) {
		security.WriteError(w, r, http.StatusUnauthorized, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
		return
	} else if errors.Is(err, ErrPrivateTestRequired) {
		security.WriteError(w, r, http.StatusBadRequest, "PRIVATE_TEST_REQUIRED", "Confirm the private Access-Request and accounting test before activation.")
		return
	} else if errors.Is(err, ErrNotFound) {
		security.WriteError(w, r, http.StatusNotFound, "ROUTER_NOT_FOUND", "Router AAA configuration was not found.")
		return
	} else if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is temporarily unavailable.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) export(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !principal.HasPermission("network.write") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to manage network AAA.")
		return
	}
	if h.lifecycle == nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is not configured.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	var input struct {
		Password string `json:"password"`
		MFACode  string `json:"mfa_code"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Password and authenticator confirmation are required.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return
	}
	setup, err := h.lifecycle.Export(r.Context(), principal, h.mutationActor(r, principal.UserID), input.Password, input.MFACode, r.PathValue("routerID"))
	if errors.Is(err, ErrStepUpFailed) {
		security.WriteError(w, r, http.StatusUnauthorized, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "NETWORK_AAA_UNAVAILABLE", "Router onboarding is temporarily unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="netcore-router-aaa-`+r.PathValue("routerID")+`.txt"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(setup)
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	options, err := h.listOptions(r)
	if err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE", "Page parameters are invalid.")
		return
	}

	page, err := h.store.List(r.Context(), principal.TenantID, options)
	if err != nil {
		if errors.Is(err, ErrInvalidPage) {
			security.WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE", "Page parameters are invalid.")
			return
		}
		security.WriteError(w, r, http.StatusServiceUnavailable, "ROUTERS_UNAVAILABLE", "Router data is temporarily unavailable.")
		return
	}

	response := listResponse{
		Data: make([]routerResponse, 0, len(page.Routers)),
		Meta: pageMeta{HasMore: page.HasMore},
	}
	for _, router := range page.Routers {
		response.Data = append(response.Data, responseRouter(router))
	}
	if page.HasMore {
		response.Meta.NextCursor = encodeCursor(page.Next)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *HTTP) listOptions(r *http.Request) (ListOptions, error) {
	query := r.URL.Query()
	search := strings.TrimSpace(query.Get("q"))
	if len(search) > maxSearchLength {
		return ListOptions{}, ErrInvalidPage
	}

	limit := h.defaultPageSize
	if rawLimit := query.Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > h.maxPageSize {
			return ListOptions{}, ErrInvalidPage
		}
		limit = parsed
	}

	status := RouterStatus(query.Get("status"))
	if status != "" && !IsValidRouterStatus(status) {
		return ListOptions{}, ErrInvalidPage
	}
	cursor, err := decodeCursor(query.Get("cursor"))
	if err != nil {
		return ListOptions{}, ErrInvalidPage
	}
	return ListOptions{Limit: limit, Cursor: cursor, Search: search, Status: status}, nil
}

type listResponse struct {
	Data []routerResponse `json:"data"`
	Meta pageMeta         `json:"meta"`
}

type pageMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// routerResponse intentionally omits management_ip, api_endpoint,
// serial_number, credential_ref, and radius_secret_ref.
type routerResponse struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	SiteName    string       `json:"site_name"`
	Status      RouterStatus `json:"status"`
	AAAStatus   string       `json:"aaa_status"`
	AAAVerified bool         `json:"aaa_verified"`
	LastSeenAt  *time.Time   `json:"last_seen_at,omitempty"`
}

func responseRouter(router Router) routerResponse {
	return routerResponse{
		ID:          router.ID,
		Name:        router.Name,
		SiteName:    router.SiteName,
		Status:      router.Status,
		AAAStatus:   router.AAAStatus,
		AAAVerified: router.AAAVerified,
		LastSeenAt:  router.LastSeenAt,
	}
}

type cursorPayload struct {
	Name string `json:"name"`
}

func encodeCursor(cursor Cursor) string {
	if cursor.IsZero() {
		return ""
	}
	raw, err := json.Marshal(cursorPayload{Name: cursor.Name})
	if err != nil {
		panic(fmt.Sprintf("network: encode cursor: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(encoded string) (Cursor, error) {
	if encoded == "" {
		return Cursor{}, nil
	}
	if len(encoded) > 512 {
		return Cursor{}, ErrInvalidPage
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return Cursor{}, ErrInvalidPage
	}
	var payload cursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return Cursor{}, ErrInvalidPage
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Cursor{}, ErrInvalidPage
	}
	if payload.Name == "" || len(payload.Name) > 256 {
		return Cursor{}, ErrInvalidPage
	}
	return Cursor{Name: payload.Name}, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
