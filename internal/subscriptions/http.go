package subscriptions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	grantStore      GrantStore
	revocationStore GrantRevocationStore
	transferStore   TransferStore
	stepUp          interface {
		VerifyStepUp(context.Context, auth.StepUpInput) error
	}
	transfersEnabled bool
	defaultPageSize  int
	maxPageSize      int
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
		grantStore:      func() GrantStore { value, _ := store.(GrantStore); return value }(),
		revocationStore: func() GrantRevocationStore { value, _ := store.(GrantRevocationStore); return value }(),
		transferStore:   func() TransferStore { value, _ := store.(TransferStore); return value }(),
		defaultPageSize: defaultPageSize,
		maxPageSize:     maxPageSize,
	}, nil
}

// ConfigureTransfers keeps device moves disabled unless explicitly enabled at startup.
func (h *HTTP) ConfigureTransfers(verifier interface {
	VerifyStepUp(context.Context, auth.StepUpInput) error
}, enabled bool) {
	h.stepUp = verifier
	h.transfersEnabled = enabled
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
	mux.Handle("POST /api/v1/customers/{id}/subscriptions/grant", sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("subscription.write", http.HandlerFunc(h.grant)))))
	mux.Handle("POST /api/v1/subscriptions/{id}/revoke-grant", sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("subscription.write", http.HandlerFunc(h.revokeGrant)))))
	mux.Handle("POST /api/v1/subscriptions/{id}/transfer-device", sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("subscription.write", http.HandlerFunc(h.transferDevice)))))
	return nil
}

func (h *HTTP) transferDevice(w http.ResponseWriter, r *http.Request) {
	if !h.transfersEnabled || h.stepUp == nil || h.transferStore == nil {
		security.WriteError(w, r, http.StatusNotFound, "TRANSFER_UNAVAILABLE", "Device transfer is not available.")
		return
	}
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" || principal.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	var input struct {
		TargetDeviceID string `json:"target_device_id"`
		Reason         string `json:"reason"`
		Password       string `json:"password"`
		MFACode        string `json:"mfa_code"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !validUUID(input.TargetDeviceID) || !validUUID(r.PathValue("id")) || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 240 || input.Password == "" || input.MFACode == "" {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_TRANSFER", "Choose a registered device and enter a reason, password, and authenticator code.")
		return
	}
	if err := h.stepUp.VerifyStepUp(r.Context(), auth.StepUpInput{Principal: principal, Password: input.Password, MFACode: input.MFACode}); err != nil {
		security.WriteError(w, r, http.StatusForbidden, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
		return
	}
	err := h.transferStore.Transfer(r.Context(), principal.TenantID, GrantActor{UserID: principal.UserID, IPAddress: sessionsClientIP(r), UserAgent: r.UserAgent()}, r.PathValue("id"), input.TargetDeviceID, input.Reason)
	switch {
	case errors.Is(err, ErrTransferHasOpenSession):
		security.WriteError(w, r, http.StatusConflict, "TRANSFER_SESSION_ACTIVE", "This plan has an open network session. Disconnect the old device before transferring.")
	case errors.Is(err, ErrTransferTargetNotFound), errors.Is(err, ErrTransferNotEligible):
		security.WriteError(w, r, http.StatusConflict, "TRANSFER_NOT_ELIGIBLE", "This plan or device is not eligible for transfer.")
	case err != nil:
		slog.Error("staff device transfer failed", slog.String("error", err.Error()))
		security.WriteError(w, r, http.StatusServiceUnavailable, "SUBSCRIPTIONS_UNAVAILABLE", "Device transfer is temporarily unavailable.")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"transferred": true})
	}
}

func (h *HTTP) revokeGrant(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" || principal.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if h.revocationStore == nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "SUBSCRIPTIONS_UNAVAILABLE", "Grant revocation is temporarily unavailable.")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REVOCATION", "A revocation reason is required.")
		return
	}
	err := h.revocationStore.RevokeGrant(r.Context(), principal.TenantID, GrantActor{UserID: principal.UserID, IPAddress: sessionsClientIP(r), UserAgent: r.UserAgent()}, r.PathValue("id"), input.Reason)
	switch {
	case errors.Is(err, ErrInvalidGrant):
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REVOCATION", "A revocation reason is required.")
	case errors.Is(err, ErrGrantTargetNotFound):
		security.WriteError(w, r, http.StatusNotFound, "GRANT_NOT_FOUND", "An active staff grant was not found.")
	case errors.Is(err, ErrGrantHasOpenSession):
		security.WriteError(w, r, http.StatusConflict, "GRANT_SESSION_ACTIVE", "This device has an open network session. Disconnect it at the router before revoking the grant.")
	case err != nil:
		slog.Error("staff subscription grant revocation failed", slog.String("error", err.Error()))
		security.WriteError(w, r, http.StatusServiceUnavailable, "SUBSCRIPTIONS_UNAVAILABLE", "Grant revocation is temporarily unavailable.")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
	}
}

func (h *HTTP) grant(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" || principal.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if h.grantStore == nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "SUBSCRIPTIONS_UNAVAILABLE", "Subscription access is temporarily unavailable.")
		return
	}
	var input GrantInput
	decoder := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_GRANT", "Choose an active plan, registered device, and grant reason.")
		return
	}
	input.CustomerID = r.PathValue("id")
	grant, err := h.grantStore.Grant(r.Context(), principal.TenantID, GrantActor{UserID: principal.UserID, IPAddress: sessionsClientIP(r), UserAgent: r.UserAgent()}, input)
	if errors.Is(err, ErrInvalidGrant) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_GRANT", "Choose an active plan, registered device, and grant reason.")
		return
	}
	if errors.Is(err, ErrGrantTargetNotFound) {
		security.WriteError(w, r, http.StatusNotFound, "GRANT_TARGET_NOT_FOUND", "The customer, plan, or device is not available.")
		return
	}
	if err != nil {
		// Keep the response generic, but retain the database error server-side so
		// an operator can diagnose a failed audited grant without exposing it.
		slog.Error("staff subscription grant failed", slog.String("error", err.Error()))
		security.WriteError(w, r, http.StatusServiceUnavailable, "SUBSCRIPTIONS_UNAVAILABLE", "Subscription access is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusCreated, responseSubscription(grant))
}

func sessionsClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return ""
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
		Meta: pageMeta{HasMore: page.HasMore, DeviceReplacementEnabled: h.transfersEnabled},
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
	NextCursor               string `json:"next_cursor,omitempty"`
	HasMore                  bool   `json:"has_more"`
	DeviceReplacementEnabled bool   `json:"device_replacement_enabled"`
}

// subscriptionResponse allowlists data needed by the operations screen.
type subscriptionResponse struct {
	ID            string                       `json:"id"`
	Customer      subscriptionCustomerResponse `json:"customer"`
	Plan          subscriptionPlanResponse     `json:"plan"`
	Device        *subscriptionDeviceResponse  `json:"device"`
	RemainingBytes *int64                    `json:"remaining_bytes"`
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

type subscriptionDeviceResponse struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	NormalizedMAC string `json:"normalized_mac"`
}

func responseSubscription(subscription Subscription) subscriptionResponse {
	response := subscriptionResponse{
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
		RemainingBytes: subscription.RemainingBytes,
		CreatedAt:     subscription.CreatedAt,
		UpdatedAt:     subscription.UpdatedAt,
	}
	if subscription.DeviceID != "" {
		response.Device = &subscriptionDeviceResponse{ID: subscription.DeviceID, Label: subscription.DeviceLabel, NormalizedMAC: subscription.DeviceMAC}
	}
	return response
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
