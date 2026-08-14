package sessions

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

// HTTP exposes staff-only access-session read endpoints.
type HTTP struct {
	store           Store
	defaultPageSize int
	maxPageSize     int
}

func NewHTTP(store Store, defaultPageSize, maxPageSize int) (*HTTP, error) {
	if store == nil {
		return nil, errors.New("sessions: store is required")
	}
	if defaultPageSize < 1 || maxPageSize < defaultPageSize {
		return nil, errors.New("sessions: invalid page size configuration")
	}
	return &HTTP{
		store:           store,
		defaultPageSize: defaultPageSize,
		maxPageSize:     maxPageSize,
	}, nil
}

// Routes keeps session read access separate from future disconnect or CoA
// operations, which must require session.write and durable audit records.
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("sessions: mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/sessions",
		sessions.RequireAuth(auth.RequirePermission("session.read", http.HandlerFunc(h.list))),
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
		security.WriteError(w, r, http.StatusServiceUnavailable, "SESSIONS_UNAVAILABLE", "Session data is temporarily unavailable.")
		return
	}

	response := listResponse{
		Data: make([]sessionResponse, 0, len(page.Sessions)),
		Meta: pageMeta{HasMore: page.HasMore},
	}
	for _, session := range page.Sessions {
		response.Data = append(response.Data, responseSession(session))
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

	status := Status(query.Get("status"))
	if status != "" && !IsValidStatus(status) {
		return ListOptions{}, ErrInvalidPage
	}
	cursor, err := decodeCursor(query.Get("cursor"))
	if err != nil {
		return ListOptions{}, ErrInvalidPage
	}
	return ListOptions{Limit: limit, Cursor: cursor, Search: search, Status: status}, nil
}

type listResponse struct {
	Data []sessionResponse `json:"data"`
	Meta pageMeta          `json:"meta"`
}

type pageMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// sessionResponse intentionally excludes acct_session_id, acct_unique_id,
// NAS address, MAC address, and termination controls. Those remain confined to
// audited operational backends rather than the browser data surface.
type sessionResponse struct {
	ID            string                  `json:"id"`
	Customer      sessionCustomerResponse `json:"customer"`
	PlanName      string                  `json:"plan_name"`
	Router        sessionRouterResponse   `json:"router"`
	IPAddress     string                  `json:"ip_address,omitempty"`
	Status        Status                  `json:"status"`
	StartedAt     time.Time               `json:"started_at"`
	LastInterimAt time.Time               `json:"last_interim_at"`
	SessionBytes  string                  `json:"session_bytes"`
	Usage         sessionUsageResponse    `json:"usage"`
}

type sessionCustomerResponse struct {
	ID             string `json:"id"`
	CustomerNumber string `json:"customer_number"`
	FirstName      string `json:"first_name,omitempty"`
	LastName       string `json:"last_name,omitempty"`
}

type sessionRouterResponse struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
}

type sessionUsageResponse struct {
	ConsumedBytes string `json:"consumed_bytes,omitempty"`
	QuotaBytes    string `json:"quota_bytes,omitempty"`
}

func responseSession(session Session) sessionResponse {
	return sessionResponse{
		ID: session.ID,
		Customer: sessionCustomerResponse{
			ID:             session.CustomerID,
			CustomerNumber: session.CustomerNumber,
			FirstName:      session.CustomerFirstName,
			LastName:       session.CustomerLastName,
		},
		PlanName:      session.PlanName,
		Router:        sessionRouterResponse{ID: session.RouterID, Name: session.RouterName},
		IPAddress:     session.IPAddress,
		Status:        session.Status,
		StartedAt:     session.StartedAt,
		LastInterimAt: session.LastInterimAt,
		SessionBytes:  session.SessionBytes,
		Usage: sessionUsageResponse{
			ConsumedBytes: session.UsageConsumedBytes,
			QuotaBytes:    session.UsageQuotaBytes,
		},
	}
}

type cursorPayload struct {
	StartedAt string `json:"started_at"`
	ID        string `json:"id"`
}

func encodeCursor(cursor Cursor) string {
	if cursor.IsZero() {
		return ""
	}
	raw, err := json.Marshal(cursorPayload{
		StartedAt: cursor.StartedAt.UTC().Format(time.RFC3339Nano),
		ID:        cursor.ID,
	})
	if err != nil {
		panic(fmt.Sprintf("sessions: encode cursor: %v", err))
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
	startedAt, err := time.Parse(time.RFC3339Nano, payload.StartedAt)
	if err != nil || !validUUID(payload.ID) {
		return Cursor{}, ErrInvalidPage
	}
	return Cursor{StartedAt: startedAt.UTC(), ID: payload.ID}, nil
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if !((value[i] >= '0' && value[i] <= '9') || (value[i] >= 'a' && value[i] <= 'f') || (value[i] >= 'A' && value[i] <= 'F')) {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
