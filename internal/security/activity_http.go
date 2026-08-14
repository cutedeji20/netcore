package security

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxActivitySearchLength = 120

// ActivityHTTP exposes the read-only Security Center activity feed.
type ActivityHTTP struct {
	store             ActivityStore
	tenantFromContext ActivityTenantLookup
	defaultPageSize   int
	maxPageSize       int
}

// ActivityTenantLookup extracts a tenant after the API authentication boundary
// has verified the browser session. Keeping it injected avoids coupling the
// security package back to auth, which already depends on security errors.
type ActivityTenantLookup func(context.Context) (tenantID string, ok bool)

func NewActivityHTTP(store ActivityStore, tenantFromContext ActivityTenantLookup, defaultPageSize, maxPageSize int) (*ActivityHTTP, error) {
	if store == nil {
		return nil, errors.New("security: activity store is required")
	}
	if tenantFromContext == nil {
		return nil, errors.New("security: activity tenant lookup is required")
	}
	if defaultPageSize < 1 || maxPageSize < defaultPageSize {
		return nil, errors.New("security: invalid activity page size configuration")
	}
	return &ActivityHTTP{
		store:             store,
		tenantFromContext: tenantFromContext,
		defaultPageSize:   defaultPageSize,
		maxPageSize:       maxPageSize,
	}, nil
}

// Routes keeps audit viewing separate from any incident acknowledgement,
// account lock, session revocation, or policy change write workflows.
func (h *ActivityHTTP) Routes(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler, requirePermission func(string, http.Handler) http.Handler) error {
	if mux == nil || requireAuth == nil || requirePermission == nil {
		return errors.New("security: mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/security/events",
		requireAuth(requirePermission("security.read", http.HandlerFunc(h.list))),
	)
	return nil
}

func (h *ActivityHTTP) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantFromContext(r.Context())
	if !ok || tenantID == "" {
		WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	options, err := h.listOptions(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE", "Page parameters are invalid.")
		return
	}

	page, err := h.store.ListActivity(r.Context(), tenantID, options)
	if err != nil {
		if errors.Is(err, ErrInvalidActivityPage) {
			WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE", "Page parameters are invalid.")
			return
		}
		WriteError(w, r, http.StatusServiceUnavailable, "SECURITY_ACTIVITY_UNAVAILABLE", "Security activity is temporarily unavailable.")
		return
	}

	response := activityListResponse{
		Data: make([]activityEventResponse, 0, len(page.Events)),
		Meta: activityPageMeta{HasMore: page.HasMore},
	}
	for _, event := range page.Events {
		response.Data = append(response.Data, responseActivityEvent(event))
	}
	if page.HasMore {
		response.Meta.NextCursor = encodeActivityCursor(page.Next)
	}
	writeActivityJSON(w, http.StatusOK, response)
}

func (h *ActivityHTTP) listOptions(r *http.Request) (ActivityListOptions, error) {
	query := r.URL.Query()
	search := strings.TrimSpace(query.Get("q"))
	if len(search) > maxActivitySearchLength {
		return ActivityListOptions{}, ErrInvalidActivityPage
	}

	limit := h.defaultPageSize
	if rawLimit := query.Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > h.maxPageSize {
			return ActivityListOptions{}, ErrInvalidActivityPage
		}
		limit = parsed
	}
	cursor, err := decodeActivityCursor(query.Get("cursor"))
	if err != nil {
		return ActivityListOptions{}, ErrInvalidActivityPage
	}
	return ActivityListOptions{Limit: limit, Cursor: cursor, Search: search}, nil
}

type activityListResponse struct {
	Data []activityEventResponse `json:"data"`
	Meta activityPageMeta        `json:"meta"`
}

type activityPageMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// activityEventResponse is safe for an operator's browser. The source audit
// row's IP, user agent, request ID, metadata, actor ID, and resource ID do
// not cross this boundary.
type activityEventResponse struct {
	ID           string    `json:"id"`
	Action       string    `json:"action"`
	Actor        string    `json:"actor"`
	ResourceType string    `json:"resource_type"`
	CreatedAt    time.Time `json:"created_at"`
}

func responseActivityEvent(event ActivityEvent) activityEventResponse {
	return activityEventResponse{
		ID:           event.ID,
		Action:       event.Action,
		Actor:        event.Actor,
		ResourceType: event.ResourceType,
		CreatedAt:    event.CreatedAt,
	}
}

type activityCursorPayload struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeActivityCursor(cursor ActivityCursor) string {
	if cursor.IsZero() {
		return ""
	}
	raw, err := json.Marshal(activityCursorPayload{
		CreatedAt: cursor.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:        cursor.ID,
	})
	if err != nil {
		panic(fmt.Sprintf("security: encode activity cursor: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeActivityCursor(encoded string) (ActivityCursor, error) {
	if encoded == "" {
		return ActivityCursor{}, nil
	}
	if len(encoded) > 512 {
		return ActivityCursor{}, ErrInvalidActivityPage
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return ActivityCursor{}, ErrInvalidActivityPage
	}
	var payload activityCursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return ActivityCursor{}, ErrInvalidActivityPage
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ActivityCursor{}, ErrInvalidActivityPage
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil || !validActivityUUID(payload.ID) {
		return ActivityCursor{}, ErrInvalidActivityPage
	}
	return ActivityCursor{CreatedAt: createdAt.UTC(), ID: payload.ID}, nil
}

func validActivityUUID(value string) bool {
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

func writeActivityJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
