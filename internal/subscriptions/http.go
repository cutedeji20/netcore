package subscriptions

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

// HTTP exposes staff-only subscription read endpoints.
type HTTP struct {
	store           Store
	defaultPageSize int
	maxPageSize     int
}

func NewHTTP(store Store, defaultPageSize, maxPageSize int) (*HTTP, error) {
	if store == nil {
		return nil, errors.New("subscriptions: store is required")
	}
	if defaultPageSize < 1 || maxPageSize < defaultPageSize {
		return nil, errors.New("subscriptions: invalid page size configuration")
	}
	return &HTTP{
		store:           store,
		defaultPageSize: defaultPageSize,
		maxPageSize:     maxPageSize,
	}, nil
}

// Routes installs the subscription list behind both session authentication and
// its own explicit permission. Lifecycle mutations will use subscription.write.
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("subscriptions: mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/subscriptions",
		sessions.RequireAuth(auth.RequirePermission("subscription.read", http.HandlerFunc(h.list))),
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
		security.WriteError(w, r, http.StatusServiceUnavailable, "SUBSCRIPTIONS_UNAVAILABLE", "Subscription data is temporarily unavailable.")
		return
	}

	response := listResponse{
		Data: make([]subscriptionResponse, 0, len(page.Subscriptions)),
		Meta: pageMeta{HasMore: page.HasMore},
	}
	for _, subscription := range page.Subscriptions {
		response.Data = append(response.Data, responseSubscription(subscription))
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
	if status != "" && !IsValid(status) {
		return ListOptions{}, ErrInvalidPage
	}
	cursor, err := decodeCursor(query.Get("cursor"))
	if err != nil {
		return ListOptions{}, ErrInvalidPage
	}
	return ListOptions{Limit: limit, Cursor: cursor, Search: search, Status: status}, nil
}

type listResponse struct {
	Data []subscriptionResponse `json:"data"`
	Meta pageMeta               `json:"meta"`
}

type pageMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// subscriptionResponse allowlists data needed by the operations screen.
type subscriptionResponse struct {
	ID            string                       `json:"id"`
	Customer      subscriptionCustomerResponse `json:"customer"`
	Plan          subscriptionPlanResponse     `json:"plan"`
	Status        Status                       `json:"status"`
	StartsAt      *time.Time                   `json:"starts_at,omitempty"`
	ExpiresAt     *time.Time                   `json:"expires_at,omitempty"`
	AutoRenew     bool                         `json:"auto_renew"`
	PaymentStatus string                       `json:"payment_status"`
	CreatedAt     time.Time                    `json:"created_at"`
	UpdatedAt     time.Time                    `json:"updated_at"`
}

type subscriptionCustomerResponse struct {
	ID             string `json:"id"`
	CustomerNumber string `json:"customer_number"`
	FirstName      string `json:"first_name,omitempty"`
	LastName       string `json:"last_name,omitempty"`
}

type subscriptionPlanResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func responseSubscription(subscription Subscription) subscriptionResponse {
	return subscriptionResponse{
		ID: subscription.ID,
		Customer: subscriptionCustomerResponse{
			ID:             subscription.CustomerID,
			CustomerNumber: subscription.CustomerNumber,
			FirstName:      subscription.CustomerFirstName,
			LastName:       subscription.CustomerLastName,
		},
		Plan: subscriptionPlanResponse{
			ID:   subscription.PlanID,
			Name: subscription.PlanName,
		},
		Status:        subscription.Status,
		StartsAt:      subscription.StartsAt,
		ExpiresAt:     subscription.ExpiresAt,
		AutoRenew:     subscription.AutoRenew,
		PaymentStatus: subscription.PaymentStatus,
		CreatedAt:     subscription.CreatedAt,
		UpdatedAt:     subscription.UpdatedAt,
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
		panic(fmt.Sprintf("subscriptions: encode cursor: %v", err))
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
