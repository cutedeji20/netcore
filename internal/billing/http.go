package billing

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

// HTTP exposes staff-only revenue transaction read endpoints.
type HTTP struct {
	store           Store
	defaultPageSize int
	maxPageSize     int
}

func NewHTTP(store Store, defaultPageSize, maxPageSize int) (*HTTP, error) {
	if store == nil {
		return nil, errors.New("billing: store is required")
	}
	if defaultPageSize < 1 || maxPageSize < defaultPageSize {
		return nil, errors.New("billing: invalid page size configuration")
	}
	return &HTTP{
		store:           store,
		defaultPageSize: defaultPageSize,
		maxPageSize:     maxPageSize,
	}, nil
}

// Routes protects the unified transaction list with its own permission.
// Payment initiation, verification, invoice issue, and refund flows must use
// separate write permissions and durable audit/outbox handling.
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("billing: mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/billing/transactions",
		sessions.RequireAuth(auth.RequirePermission("billing.read", http.HandlerFunc(h.list))),
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
		security.WriteError(w, r, http.StatusServiceUnavailable, "BILLING_UNAVAILABLE", "Billing data is temporarily unavailable.")
		return
	}

	response := listResponse{
		Data: make([]transactionResponse, 0, len(page.Transactions)),
		Meta: pageMeta{HasMore: page.HasMore},
	}
	for _, transaction := range page.Transactions {
		response.Data = append(response.Data, responseTransaction(transaction))
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

	source := Source(query.Get("source"))
	if source != "" && !IsValidSource(source) {
		return ListOptions{}, ErrInvalidPage
	}
	cursor, err := decodeCursor(query.Get("cursor"))
	if err != nil {
		return ListOptions{}, ErrInvalidPage
	}
	return ListOptions{Limit: limit, Cursor: cursor, Search: search, Source: source}, nil
}

type listResponse struct {
	Data []transactionResponse `json:"data"`
	Meta pageMeta              `json:"meta"`
}

type pageMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// transactionResponse has no gateway API key, webhook payload, provider
// signature, or subscription control field. AmountMinor remains a decimal
// string to preserve exact money values in JavaScript clients.
type transactionResponse struct {
	ID          string              `json:"id"`
	Source      Source              `json:"source"`
	Reference   string              `json:"reference"`
	Customer    transactionCustomer `json:"customer"`
	AmountMinor string              `json:"amount_minor"`
	Currency    string              `json:"currency"`
	Status      string              `json:"status"`
	RecordedAt  time.Time           `json:"recorded_at"`
	DueAt       *time.Time          `json:"due_at,omitempty"`
	VerifiedAt  *time.Time          `json:"verified_at,omitempty"`
}

type transactionCustomer struct {
	ID             string `json:"id"`
	CustomerNumber string `json:"customer_number"`
	FirstName      string `json:"first_name,omitempty"`
	LastName       string `json:"last_name,omitempty"`
}

func responseTransaction(transaction Transaction) transactionResponse {
	return transactionResponse{
		ID:        transaction.ID,
		Source:    transaction.Source,
		Reference: transaction.Reference,
		Customer: transactionCustomer{
			ID:             transaction.CustomerID,
			CustomerNumber: transaction.CustomerNumber,
			FirstName:      transaction.CustomerFirstName,
			LastName:       transaction.CustomerLastName,
		},
		AmountMinor: strconv.FormatInt(transaction.AmountMinor, 10),
		Currency:    transaction.Currency,
		Status:      transaction.Status,
		RecordedAt:  transaction.RecordedAt,
		DueAt:       transaction.DueAt,
		VerifiedAt:  transaction.VerifiedAt,
	}
}

type cursorPayload struct {
	RecordedAt string `json:"recorded_at"`
	Source     Source `json:"source"`
	ID         string `json:"id"`
}

func encodeCursor(cursor Cursor) string {
	if cursor.IsZero() {
		return ""
	}
	raw, err := json.Marshal(cursorPayload{
		RecordedAt: cursor.RecordedAt.UTC().Format(time.RFC3339Nano),
		Source:     cursor.Source,
		ID:         cursor.ID,
	})
	if err != nil {
		panic(fmt.Sprintf("billing: encode cursor: %v", err))
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
	recordedAt, err := time.Parse(time.RFC3339Nano, payload.RecordedAt)
	if err != nil || !IsValidSource(payload.Source) || !validUUID(payload.ID) {
		return Cursor{}, ErrInvalidPage
	}
	return Cursor{RecordedAt: recordedAt.UTC(), Source: payload.Source, ID: payload.ID}, nil
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
