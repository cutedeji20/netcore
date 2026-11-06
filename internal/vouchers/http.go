package vouchers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

var voucherSummaryPeriods = map[string]struct{}{
	"today": {}, "7d": {}, "30d": {}, "this_month": {}, "last_month": {}, "all": {},
}

var voucherBatchStatuses = map[string]struct{}{
	"": {}, "ACTIVE": {}, "COMPLETED": {}, "EXPIRED": {}, "REVOKED": {}, "MIXED": {},
}

// HTTP exposes staff-only voucher inventory read endpoints.
type HTTP struct {
	store           Store
	defaultPageSize int
	maxPageSize     int
	staff           *StaffService
	revealLimiter   interface {
		AllowSlidingWindow(context.Context, string, int64, time.Duration) (bool, error)
	}
}

func NewHTTP(store Store, defaultPageSize, maxPageSize int) (*HTTP, error) {
	if store == nil {
		return nil, errors.New("vouchers: store is required")
	}
	if defaultPageSize < 1 || maxPageSize < defaultPageSize {
		return nil, errors.New("vouchers: invalid page size configuration")
	}
	return &HTTP{
		store:           store,
		defaultPageSize: defaultPageSize,
		maxPageSize:     maxPageSize,
	}, nil
}

// Routes leaves batch creation, code delivery, redemption, and revocation for
// voucher.write flows with durable audit and idempotency controls.
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("vouchers: mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/vouchers/batches",
		sessions.RequireAuth(auth.RequirePermission("voucher.read", http.HandlerFunc(h.list))),
	)
	mux.Handle(
		"GET /api/v1/vouchers/summary",
		sessions.RequireAuth(auth.RequirePermission("voucher.read", http.HandlerFunc(h.summary))),
	)
	if h.staff != nil {
		mux.Handle("GET /api/v1/vouchers/my-batches", sessions.RequireAuth(http.HandlerFunc(h.listBuyer)))
		mux.Handle("POST /api/v1/vouchers/batches", sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("voucher.write", http.HandlerFunc(h.issueBatch)))))
		mux.Handle("POST /api/v1/vouchers/batches/delete-unused", sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("voucher.write", http.HandlerFunc(h.deleteUnusedBatches)))))
		mux.Handle("POST /api/v1/vouchers/{id}/revoke", sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("voucher.write", http.HandlerFunc(h.revokeVoucher)))))
		mux.Handle("POST /api/v1/vouchers/batches/{id}/reveal", sessions.RequireAuth(sessions.RequireAllowedOrigin(http.HandlerFunc(h.revealBatch))))
		mux.Handle("POST /api/v1/vouchers/batches/{id}/print", sessions.RequireAuth(sessions.RequireAllowedOrigin(http.HandlerFunc(h.printBatch))))
	}
	return nil
}

func (h *HTTP) summary(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	period, startsAt, endsAt, err := voucherSummaryPeriod(r.URL.Query().Get("period"), time.Now())
	if err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_PERIOD", "Voucher summary period is invalid.")
		return
	}
	summary, err := h.store.Summary(r.Context(), principal.TenantID, startsAt, endsAt, period)
	if err != nil {
		if errors.Is(err, ErrInvalidPage) {
			security.WriteError(w, r, http.StatusBadRequest, "INVALID_PERIOD", "Voucher summary period is invalid.")
			return
		}
		security.WriteError(w, r, http.StatusServiceUnavailable, "VOUCHERS_UNAVAILABLE", "Voucher summary is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, summaryResponse{Summary: summary, RedemptionRateBasisPoints: summary.RedemptionRateBasisPoints()})
}

func voucherSummaryPeriod(raw string, now time.Time) (string, time.Time, time.Time, error) {
	period := strings.ToLower(strings.TrimSpace(raw))
	if period == "" {
		period = "30d"
	}
	if _, ok := voucherSummaryPeriods[period]; !ok {
		return "", time.Time{}, time.Time{}, ErrInvalidPage
	}
	location, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	localNow := now.In(location)
	end := localNow.UTC()
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	switch period {
	case "today":
		return period, dayStart.UTC(), end, nil
	case "7d":
		return period, localNow.AddDate(0, 0, -7).UTC(), end, nil
	case "30d":
		return period, localNow.AddDate(0, 0, -30).UTC(), end, nil
	case "this_month":
		return period, time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, location).UTC(), end, nil
	case "last_month":
		thisMonth := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, location)
		return period, thisMonth.AddDate(0, -1, 0).UTC(), thisMonth.UTC(), nil
	default:
		return period, time.Unix(0, 0).UTC(), end, nil
	}
}

func (h *HTTP) listBuyer(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	options, err := h.listOptions(r)
	if err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE", "Page parameters are invalid.")
		return
	}
	store, ok := h.store.(interface {
		ListBuyerBatches(context.Context, string, string, ListOptions) (Page, error)
	})
	if !ok {
		security.WriteError(w, r, http.StatusServiceUnavailable, "VOUCHERS_UNAVAILABLE", "Voucher inventory is unavailable.")
		return
	}
	page, err := store.ListBuyerBatches(r.Context(), p.TenantID, p.UserID, options)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "VOUCHERS_UNAVAILABLE", "Voucher inventory is unavailable.")
		return
	}
	response := listResponse{Data: make([]batchResponse, 0, len(page.Batches)), Meta: pageMeta{HasMore: page.HasMore}}
	for _, batch := range page.Batches {
		response.Data = append(response.Data, responseBatch(batch))
	}
	if page.HasMore {
		response.Meta.NextCursor = encodeCursor(page.Next)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *HTTP) ConfigureRevealLimiter(limiter interface {
	AllowSlidingWindow(context.Context, string, int64, time.Duration) (bool, error)
}) {
	h.revealLimiter = limiter
}

func revealRateKey(tenantID, userID, batchID string) string {
	sum := sha256.Sum256([]byte(tenantID + "\x00" + userID + "\x00" + batchID))
	return "vouchers:reveal:" + hex.EncodeToString(sum[:])
}

func (h *HTTP) ConfigureStaff(service *StaffService) error {
	if h == nil || service == nil {
		return ErrStaffVoucherDenied
	}
	h.staff = service
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
		security.WriteError(w, r, http.StatusServiceUnavailable, "VOUCHERS_UNAVAILABLE", "Voucher data is temporarily unavailable.")
		return
	}

	response := listResponse{
		Data: make([]batchResponse, 0, len(page.Batches)),
		Meta: pageMeta{HasMore: page.HasMore},
	}
	for _, batch := range page.Batches {
		response.Data = append(response.Data, responseBatch(batch))
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
	status := strings.ToUpper(strings.TrimSpace(query.Get("status")))
	if _, ok := voucherBatchStatuses[status]; !ok {
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
	return ListOptions{Limit: limit, Cursor: cursor, Search: search, Status: status}, nil
}

type listResponse struct {
	Data []batchResponse `json:"data"`
	Meta pageMeta        `json:"meta"`
}

type pageMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

type summaryResponse struct {
	Summary                   Summary `json:"summary"`
	RedemptionRateBasisPoints int64   `json:"redemption_rate_basis_points"`
}

// batchResponse exposes aggregate operational data only: not one character of
// a voucher code, its hash, or its support prefix leaves this boundary.
type batchResponse struct {
	ID           string     `json:"id"`
	PlanName     string     `json:"plan_name"`
	OwnerDisplay string     `json:"owner_display"`
	OwnerType    string     `json:"owner_type"`
	Issued       int64      `json:"issued"`
	Redeemed     int64      `json:"redeemed"`
	Available    int64      `json:"available"`
	Status       string     `json:"status"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func responseBatch(batch Batch) batchResponse {
	return batchResponse{
		ID:           batch.ID,
		PlanName:     batch.PlanName,
		OwnerDisplay: batch.OwnerDisplay,
		OwnerType:    batch.OwnerType,
		Issued:       batch.Issued,
		Redeemed:     batch.Redeemed,
		Available:    batch.Available,
		Status:       batch.Status,
		ExpiresAt:    batch.ExpiresAt,
		CreatedAt:    batch.CreatedAt,
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
		panic(fmt.Sprintf("vouchers: encode cursor: %v", err))
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
