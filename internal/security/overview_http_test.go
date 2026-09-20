package security

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type overviewMemoryStore struct {
	tenantID string
	overview Overview
	err      error
}

func (s *overviewMemoryStore) Overview(_ context.Context, tenantID string) (Overview, error) {
	s.tenantID = tenantID
	return s.overview, s.err
}

func TestOverviewUsesAuthenticatedTenant(t *testing.T) {
	store := &overviewMemoryStore{overview: Overview{ActiveCustomers: 4, OnlineSessions: 2, CollectedTodayMinor: 51500, Attention: 1}}
	h, err := NewOverviewHTTP(store, func(ctx context.Context) (string, bool) {
		id, ok := ctx.Value(activityTestTenantKey{}).(string)
		return id, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/operations/overview", nil)
	r = r.WithContext(context.WithValue(r.Context(), activityTestTenantKey{}, activityTestTenantID))
	w := httptest.NewRecorder()
	h.get(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	if store.tenantID != activityTestTenantID {
		t.Fatalf("tenant = %q", store.tenantID)
	}
	var body struct {
		ActiveCustomers     int64 `json:"active_customers"`
		OnlineSessions      int64 `json:"online_sessions"`
		CollectedTodayMinor int64 `json:"collected_today_minor"`
		Attention           int64 `json:"attention"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ActiveCustomers != 4 || body.OnlineSessions != 2 || body.CollectedTodayMinor != 51500 || body.Attention != 1 {
		t.Fatalf("body = %+v", body)
	}
}

func TestOverviewRejectsMissingTenant(t *testing.T) {
	h, err := NewOverviewHTTP(&overviewMemoryStore{}, func(context.Context) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.get(w, httptest.NewRequest(http.MethodGet, "/api/v1/operations/overview", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
}
