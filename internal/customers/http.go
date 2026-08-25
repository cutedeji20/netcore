package customers

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

// HTTP exposes staff-only customer read endpoints.
type HTTP struct {
	store           Store
	defaultPageSize int
	maxPageSize     int
	clientIP        func(*http.Request) string
}

func NewHTTP(store Store, defaultPageSize, maxPageSize int) (*HTTP, error) {
	if store == nil {
		return nil, errors.New("customers: store is required")
	}
	if defaultPageSize < 1 || maxPageSize < defaultPageSize {
		return nil, errors.New("customers: invalid page size configuration")
	}
	return &HTTP{
		store:           store,
		defaultPageSize: defaultPageSize,
		maxPageSize:     maxPageSize,
	}, nil
}

// Routes installs the customer read route inside both session authentication
// and explicit permission enforcement. A future mutation route receives its
// own customer.write permission rather than inheriting customer.read.
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("customers: mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/customers",
		sessions.RequireAuth(auth.RequirePermission("customer.read", http.HandlerFunc(h.list))),
	)
	mux.Handle(
		"POST /api/v1/customers",
		sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("customer.write", http.HandlerFunc(h.create)))),
	)
	mux.Handle(
		"PUT /api/v1/customers/{id}",
		sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("customer.write", http.HandlerFunc(h.update)))),
	)
	mux.Handle(
		"POST /api/v1/customers/{id}/deactivate",
		sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("customer.write", http.HandlerFunc(h.deactivate)))),
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
		security.WriteError(w, r, http.StatusServiceUnavailable, "CUSTOMERS_UNAVAILABLE", "Customer data is temporarily unavailable.")
		return
	}

	response := listResponse{
		Data: make([]customerResponse, 0, len(page.Customers)),
		Meta: pageMeta{HasMore: page.HasMore},
	}
	for _, customer := range page.Customers {
		response.Data = append(response.Data, responseCustomer(customer))
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
	input, err := decodeWriteInput(r)
	if err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_CUSTOMER", "Customer details are invalid.")
		return
	}
	customer, err := h.store.Create(r.Context(), principal.TenantID, h.mutationActor(r, principal.UserID), input)
	if errors.Is(err, ErrDuplicateEmail) {
		security.WriteError(w, r, http.StatusConflict, "CUSTOMER_EMAIL_EXISTS", "A customer with this e-mail already exists.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "CUSTOMERS_UNAVAILABLE", "Customer data is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusCreated, responseCustomer(customer))
}

func (h *HTTP) update(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" || principal.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	customerID := r.PathValue("id")
	if !validUUID(customerID) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_CUSTOMER", "Customer details are invalid.")
		return
	}
	input, err := decodeWriteInput(r)
	if err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_CUSTOMER", "Customer details are invalid.")
		return
	}
	customer, err := h.store.Update(r.Context(), principal.TenantID, customerID, h.mutationActor(r, principal.UserID), input)
	if h.writeMutationError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, responseCustomer(customer))
}

func (h *HTTP) deactivate(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" || principal.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	customerID := r.PathValue("id")
	if !validUUID(customerID) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_CUSTOMER", "Customer details are invalid.")
		return
	}
	customer, err := h.store.Deactivate(r.Context(), principal.TenantID, customerID, h.mutationActor(r, principal.UserID))
	if h.writeMutationError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, responseCustomer(customer))
}

func (h *HTTP) writeMutationError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, ErrNotFound):
		security.WriteError(w, r, http.StatusNotFound, "CUSTOMER_NOT_FOUND", "The customer was not found.")
	case errors.Is(err, ErrDuplicateEmail):
		security.WriteError(w, r, http.StatusConflict, "CUSTOMER_EMAIL_EXISTS", "A customer with this e-mail already exists.")
	case err != nil:
		security.WriteError(w, r, http.StatusServiceUnavailable, "CUSTOMERS_UNAVAILABLE", "Customer data is temporarily unavailable.")
	default:
		return false
	}
	return true
}

type customerWriteRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
}

func decodeWriteInput(r *http.Request) (WriteInput, error) {
	var request customerWriteRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return WriteInput{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return WriteInput{}, errors.New("additional JSON values")
	}
	input := WriteInput{FirstName: request.FirstName, LastName: request.LastName, Email: request.Email, Phone: request.Phone}
	if err := input.NormalizeAndValidate(); err != nil {
		return WriteInput{}, err
	}
	return input, nil
}

func (h *HTTP) mutationActor(r *http.Request, userID string) MutationActor {
	return MutationActor{UserID: userID, IP: h.requestIP(r), UserAgent: r.UserAgent()}
}

func (h *HTTP) requestIP(r *http.Request) string {
	if h.clientIP != nil {
		return h.clientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
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

	cursor, err := decodeCursor(query.Get("cursor"))
	if err != nil {
		return ListOptions{}, ErrInvalidPage
	}
	return ListOptions{Limit: limit, Cursor: cursor, Search: search}, nil
}

type listResponse struct {
	Data []customerResponse `json:"data"`
	Meta pageMeta           `json:"meta"`
}

type pageMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// customerResponse is an allowlist DTO. Do not add persistence fields here
// without reviewing whether staff actually need to see them.
type customerResponse struct {
	ID             string    `json:"id"`
	CustomerNumber string    `json:"customer_number"`
	Status         string    `json:"status"`
	FirstName      string    `json:"first_name,omitempty"`
	LastName       string    `json:"last_name,omitempty"`
	Phone          string    `json:"phone,omitempty"`
	Email          string    `json:"email,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func responseCustomer(customer Customer) customerResponse {
	return customerResponse{
		ID:             customer.ID,
		CustomerNumber: customer.CustomerNumber,
		Status:         customer.Status,
		FirstName:      customer.FirstName,
		LastName:       customer.LastName,
		Phone:          customer.Phone,
		Email:          customer.Email,
		CreatedAt:      customer.CreatedAt,
		UpdatedAt:      customer.UpdatedAt,
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
		panic(fmt.Sprintf("customers: encode cursor: %v", err))
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
