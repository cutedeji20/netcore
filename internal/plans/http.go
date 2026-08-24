package plans

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

const maxSearchLength = 120

// HTTP exposes staff-only plan catalogue read endpoints.
type HTTP struct {
	store           Store
	defaultPageSize int
	maxPageSize     int
	clientIP        func(*http.Request) string
}

func NewHTTP(store Store, defaultPageSize, maxPageSize int) (*HTTP, error) {
	if store == nil {
		return nil, errors.New("plans: store is required")
	}
	if defaultPageSize < 1 || maxPageSize < defaultPageSize {
		return nil, errors.New("plans: invalid page size configuration")
	}
	return &HTTP{
		store:           store,
		defaultPageSize: defaultPageSize,
		maxPageSize:     maxPageSize,
	}, nil
}

// Routes installs the catalogue route behind authentication and its own
// permission. Plan creation and retirement will require plan.write.
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("plans: mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/plans",
		sessions.RequireAuth(auth.RequirePermission("plan.read", http.HandlerFunc(h.list))),
	)
	mux.Handle(
		"POST /api/v1/plans",
		sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("plan.write", http.HandlerFunc(h.create)))),
	)
	mux.Handle(
		"PUT /api/v1/plans/{id}",
		sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("plan.write", http.HandlerFunc(h.update)))),
	)
	h.clientIP = sessions.ClientIP
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
		security.WriteError(w, r, http.StatusServiceUnavailable, "PLANS_UNAVAILABLE", "Plan data is temporarily unavailable.")
		return
	}

	response := listResponse{
		Data: make([]planResponse, 0, len(page.Plans)),
		Meta: pageMeta{HasMore: page.HasMore},
	}
	for _, plan := range page.Plans {
		response.Data = append(response.Data, responsePlan(plan))
	}
	if page.HasMore {
		response.Meta.NextCursor = encodeCursor(page.Next)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *HTTP) create(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" || principal.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	input, err := decodeWriteInput(r, false)
	if err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_PLAN", "Plan details are invalid.")
		return
	}
	plan, err := h.store.Create(r.Context(), principal.TenantID, MutationActor{
		UserID:    principal.UserID,
		IP:        h.requestIP(r),
		UserAgent: r.UserAgent(),
	}, input)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "PLANS_UNAVAILABLE", "Plan data is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusCreated, responsePlan(plan))
}

func (h *HTTP) update(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" || principal.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	planID := r.PathValue("id")
	if !validUUID(planID) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_PLAN", "Plan details are invalid.")
		return
	}
	input, err := decodeWriteInput(r, true)
	if err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_PLAN", "Plan details are invalid.")
		return
	}
	plan, err := h.store.Update(r.Context(), principal.TenantID, planID, MutationActor{
		UserID:    principal.UserID,
		IP:        h.requestIP(r),
		UserAgent: r.UserAgent(),
	}, input)
	switch {
	case errors.Is(err, ErrNotFound):
		security.WriteError(w, r, http.StatusNotFound, "PLAN_NOT_FOUND", "The plan was not found.")
		return
	case errors.Is(err, ErrTermsLocked):
		security.WriteError(w, r, http.StatusConflict, "PLAN_TERMS_LOCKED", "Only publication status can change after a plan has subscriptions.")
		return
	case err != nil:
		security.WriteError(w, r, http.StatusServiceUnavailable, "PLANS_UNAVAILABLE", "Plan data is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, responsePlan(plan))
}

type writeRequest struct {
	Name                  string `json:"name"`
	Description           string `json:"description"`
	PriceMinor            string `json:"price_minor"`
	Currency              string `json:"currency"`
	DurationSeconds       int64  `json:"duration_seconds"`
	DownloadBPS           int64  `json:"download_bps"`
	UploadBPS             int64  `json:"upload_bps"`
	MaxDevices            int    `json:"max_devices"`
	MaxConcurrentSessions int    `json:"max_concurrent_sessions"`
	QuotaBytes            *int64 `json:"quota_bytes"`
	QuotaResetPolicy      string `json:"quota_reset_policy"`
	Status                Status `json:"status"`
}

func decodeWriteInput(r *http.Request, requireStatus bool) (WriteInput, error) {
	var request writeRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return WriteInput{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return WriteInput{}, errors.New("additional JSON values")
	}
	if requireStatus && strings.TrimSpace(string(request.Status)) == "" {
		return WriteInput{}, ErrInvalidInput
	}
	priceMinor, err := strconv.ParseInt(strings.TrimSpace(request.PriceMinor), 10, 64)
	if err != nil {
		return WriteInput{}, ErrInvalidInput
	}
	input := WriteInput{
		Name:                  request.Name,
		Description:           request.Description,
		PriceMinor:            priceMinor,
		Currency:              request.Currency,
		DurationSeconds:       request.DurationSeconds,
		DownloadBPS:           request.DownloadBPS,
		UploadBPS:             request.UploadBPS,
		MaxDevices:            request.MaxDevices,
		MaxConcurrentSessions: request.MaxConcurrentSessions,
		QuotaBytes:            request.QuotaBytes,
		QuotaResetPolicy:      request.QuotaResetPolicy,
		Status:                request.Status,
	}
	if err := input.NormalizeAndValidate(); err != nil {
		return WriteInput{}, err
	}
	return input, nil
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
}

func (h *HTTP) requestIP(r *http.Request) string {
	if h.clientIP != nil {
		return h.clientIP(r)
	}
	return requestIP(r)
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
	Data []planResponse `json:"data"`
	Meta pageMeta       `json:"meta"`
}

type pageMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// planResponse exposes the plan terms staff need without leaking tenant data.
// PriceMinor is a decimal string so every consumer retains exact minor-unit
// precision instead of silently rounding an int64 through a JSON number.
type planResponse struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Description           string    `json:"description,omitempty"`
	PriceMinor            string    `json:"price_minor"`
	Currency              string    `json:"currency"`
	DurationSeconds       int64     `json:"duration_seconds"`
	DownloadBPS           int64     `json:"download_bps"`
	UploadBPS             int64     `json:"upload_bps"`
	MaxDevices            int       `json:"max_devices"`
	MaxConcurrentSessions int       `json:"max_concurrent_sessions"`
	QuotaBytes            *int64    `json:"quota_bytes,omitempty"`
	QuotaResetPolicy      string    `json:"quota_reset_policy"`
	Status                Status    `json:"status"`
	ActiveSubscriptions   int64     `json:"active_subscriptions"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

func responsePlan(plan Plan) planResponse {
	return planResponse{
		ID:                    plan.ID,
		Name:                  plan.Name,
		Description:           plan.Description,
		PriceMinor:            strconv.FormatInt(plan.PriceMinor, 10),
		Currency:              plan.Currency,
		DurationSeconds:       plan.DurationSeconds,
		DownloadBPS:           plan.DownloadBPS,
		UploadBPS:             plan.UploadBPS,
		MaxDevices:            plan.MaxDevices,
		MaxConcurrentSessions: plan.MaxConcurrentSessions,
		QuotaBytes:            plan.QuotaBytes,
		QuotaResetPolicy:      plan.QuotaResetPolicy,
		Status:                plan.Status,
		ActiveSubscriptions:   plan.ActiveSubscriptions,
		CreatedAt:             plan.CreatedAt,
		UpdatedAt:             plan.UpdatedAt,
	}
}

type cursorPayload struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeCursor(cursor Cursor) string {
	if cursor.IsZero() {
		return ""
	}
	raw, err := json.Marshal(cursorPayload{
		CreatedAt: cursor.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:        cursor.ID,
	})
	if err != nil {
		panic(fmt.Sprintf("plans: encode cursor: %v", err))
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
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil || !validUUID(payload.ID) {
		return Cursor{}, ErrInvalidPage
	}
	return Cursor{CreatedAt: createdAt.UTC(), ID: payload.ID}, nil
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
