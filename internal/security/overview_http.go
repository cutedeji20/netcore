package security

import (
	"net/http"
	"time"
)

type OverviewHTTP struct {
	store             OverviewStore
	tenantFromContext ActivityTenantLookup
}

func NewOverviewHTTP(store OverviewStore, tenantFromContext ActivityTenantLookup) (*OverviewHTTP, error) {
	if store == nil || tenantFromContext == nil {
		return nil, ErrActivityUnavailable
	}
	return &OverviewHTTP{store, tenantFromContext}, nil
}
func (h *OverviewHTTP) Routes(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler, requirePermission func(string, http.Handler) http.Handler) error {
	if mux == nil || requireAuth == nil || requirePermission == nil {
		return ErrActivityUnavailable
	}
	mux.Handle("GET /api/v1/operations/overview", requireAuth(requirePermission("security.read", http.HandlerFunc(h.get))))
	return nil
}
func (h *OverviewHTTP) get(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenantFromContext(r.Context())
	if !ok || tenant == "" {
		WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	o, err := h.store.Overview(r.Context(), tenant)
	if err != nil {
		WriteError(w, r, http.StatusServiceUnavailable, "OVERVIEW_UNAVAILABLE", "Overview data is temporarily unavailable.")
		return
	}
	writeActivityJSON(w, http.StatusOK, struct {
		ActiveCustomers     int64               `json:"active_customers"`
		OnlineSessions      int64               `json:"online_sessions"`
		CollectedTodayMinor int64               `json:"collected_today_minor"`
		Attention           int64               `json:"attention"`
		UpdatedAt           time.Time           `json:"updated_at"`
		Customers           CustomerMetrics     `json:"customers"`
		Plans               PlanMetrics         `json:"plans"`
		Subscriptions       SubscriptionMetrics `json:"subscriptions"`
	}{o.ActiveCustomers, o.OnlineSessions, o.CollectedTodayMinor, o.Attention, time.Now().UTC(), o.CustomerMetrics, o.PlanMetrics, o.SubscriptionMetrics})
}
