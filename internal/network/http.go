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
	defaultPageSize int
	maxPageSize     int
}

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
	return nil
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
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	SiteName   string       `json:"site_name"`
	Status     RouterStatus `json:"status"`
	AAAStatus  string       `json:"aaa_status"`
	LastSeenAt *time.Time   `json:"last_seen_at,omitempty"`
}

func responseRouter(router Router) routerResponse {
	return routerResponse{
		ID:         router.ID,
		Name:       router.Name,
		SiteName:   router.SiteName,
		Status:     router.Status,
		AAAStatus:  router.AAAStatus,
		LastSeenAt: router.LastSeenAt,
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
