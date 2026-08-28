package plans

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

const planTestTenantID = "11111111-1111-4111-8111-111111111111"

type memoryStore struct {
	tenantID string
	options  ListOptions
	page     Page
	err      error

	createdTenant string
	createdActor  MutationActor
	createdInput  WriteInput
	createdPlan   Plan

	updatedTenant string
	updatedPlanID string
	updatedActor  MutationActor
	updatedInput  WriteInput
	updatedPlan   Plan

	retiredTenant string
	retiredPlanID string
	retiredActor  MutationActor
	retiredPlan   Plan
	retiredErr    error

	restoredTenant string
	restoredPlanID string
	restoredActor  MutationActor
	restoredPlan   Plan
	restoredErr    error

	deletedTenant string
	deletedPlanID string
	deletedActor  MutationActor
	deletedErr    error
}

func (s *memoryStore) List(_ context.Context, tenantID string, options ListOptions) (Page, error) {
	s.tenantID = tenantID
	s.options = options
	return s.page, s.err
}

func (s *memoryStore) Create(_ context.Context, tenantID string, actor MutationActor, input WriteInput) (Plan, error) {
	s.createdTenant = tenantID
	s.createdActor = actor
	s.createdInput = input
	return s.createdPlan, s.err
}

func (s *memoryStore) Update(_ context.Context, tenantID, planID string, actor MutationActor, input WriteInput) (Plan, error) {
	s.updatedTenant = tenantID
	s.updatedPlanID = planID
	s.updatedActor = actor
	s.updatedInput = input
	return s.updatedPlan, s.err
}

func (s *memoryStore) Retire(_ context.Context, tenantID, planID string, actor MutationActor) (Plan, error) {
	s.retiredTenant = tenantID
	s.retiredPlanID = planID
	s.retiredActor = actor
	return s.retiredPlan, s.retiredErr
}

func (s *memoryStore) Restore(_ context.Context, tenantID, planID string, actor MutationActor) (Plan, error) {
	s.restoredTenant = tenantID
	s.restoredPlanID = planID
	s.restoredActor = actor
	return s.restoredPlan, s.restoredErr
}

func (s *memoryStore) Delete(_ context.Context, tenantID, planID string, actor MutationActor) error {
	s.deletedTenant = tenantID
	s.deletedPlanID = planID
	s.deletedActor = actor
	return s.deletedErr
}

func newTestHTTP(t *testing.T) (*HTTP, *memoryStore) {
	t.Helper()
	quotaBytes := int64(1_000_000_000_000)
	store := &memoryStore{
		page: Page{
			Plans: []Plan{{
				ID:                    "22222222-2222-4222-8222-222222222222",
				Name:                  "Home Pro 50",
				Description:           "Fast home fibre",
				PriceMinor:            2_100_000,
				Currency:              "NGN",
				DurationSeconds:       2_592_000,
				DownloadBPS:           50_000_000,
				UploadBPS:             25_000_000,
				MaxDevices:            4,
				MaxConcurrentSessions: 2,
				QuotaBytes:            &quotaBytes,
				QuotaResetPolicy:      "PER_SUBSCRIPTION",
				Status:                StatusActive,
				ActiveSubscriptions:   986,
				CreatedAt:             time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
				UpdatedAt:             time.Date(2026, 8, 12, 10, 5, 0, 0, time.UTC),
			}},
		},
	}
	handler, err := NewHTTP(store, 25, 100)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

func TestListUsesTenantScopedCursorOptions(t *testing.T) {
	handler, store := newTestHTTP(t)
	cursor := encodeCursor(Cursor{
		CreatedAt: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC),
		ID:        "33333333-3333-4333-8333-333333333333",
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/plans?limit=10&q=fibre&status=ACTIVE&cursor="+cursor, nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: planTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if store.tenantID != planTestTenantID || store.options.Limit != 10 || store.options.Search != "fibre" || store.options.Status != StatusActive || store.options.Cursor.ID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("unexpected store request: tenant=%q options=%+v", store.tenantID, store.options)
	}
	var body listResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].PriceMinor != "2100000" || body.Data[0].ActiveSubscriptions != 986 || body.Data[0].QuotaBytes == nil {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestListRejectsUnsafePageParameters(t *testing.T) {
	handler, _ := newTestHTTP(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/plans?status=DRAFT", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: planTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestListRejectsMissingPrincipal(t *testing.T) {
	handler, _ := newTestHTTP(t)
	response := httptest.NewRecorder()

	handler.list(response, httptest.NewRequest(http.MethodGet, "/api/v1/plans", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestCreateUsesPrincipalTenantAndNormalizesPlanInput(t *testing.T) {
	handler, store := newTestHTTP(t)
	store.createdPlan = Plan{
		ID:                    "55555555-5555-4555-8555-555555555555",
		Name:                  "Day Pass",
		PriceMinor:            50000,
		Currency:              "NGN",
		DurationSeconds:       86400,
		DownloadBPS:           20_000_000,
		UploadBPS:             10_000_000,
		MaxDevices:            2,
		MaxConcurrentSessions: 1,
		QuotaResetPolicy:      "NONE",
		Status:                StatusActive,
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plans", strings.NewReader(`{
"name":"  Day Pass  ","description":"  One day of access  ","price_minor":"50000","currency":"ngn",
"duration_seconds":86400,"download_bps":20000000,"upload_bps":10000000,
"max_devices":2,"max_concurrent_sessions":1,"quota_reset_policy":"none","status":"active"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "NetCore test operator")
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{
		TenantID: planTestTenantID,
		UserID:   "66666666-6666-4666-8666-666666666666",
	}))
	response := httptest.NewRecorder()

	handler.create(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.createdTenant != planTestTenantID || store.createdActor.UserID != "66666666-6666-4666-8666-666666666666" {
		t.Fatalf("create scope = tenant %q actor %+v", store.createdTenant, store.createdActor)
	}
	if got := store.createdInput; got.Name != "Day Pass" || got.Description != "One day of access" || got.Currency != "NGN" || got.PriceMinor != 50000 || got.Status != StatusActive || got.QuotaBytes != nil || got.QuotaResetPolicy != "NONE" {
		t.Fatalf("normalized input = %+v", got)
	}
	var body planResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "55555555-5555-4555-8555-555555555555" || body.PriceMinor != "50000" || body.Status != StatusActive {
		t.Fatalf("response = %+v", body)
	}
}

func TestUpdateRetiresTheTenantPlan(t *testing.T) {
	handler, store := newTestHTTP(t)
	store.updatedPlan = Plan{
		ID:                    "55555555-5555-4555-8555-555555555555",
		Name:                  "Day Pass",
		PriceMinor:            50000,
		Currency:              "NGN",
		DurationSeconds:       86400,
		DownloadBPS:           20_000_000,
		UploadBPS:             10_000_000,
		MaxDevices:            2,
		MaxConcurrentSessions: 1,
		QuotaResetPolicy:      "NONE",
		Status:                StatusRetired,
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/plans/55555555-5555-4555-8555-555555555555", strings.NewReader(`{
"name":"Day Pass","description":"One day of access","price_minor":"50000","currency":"NGN",
"duration_seconds":86400,"download_bps":20000000,"upload_bps":10000000,
"max_devices":2,"max_concurrent_sessions":1,"quota_reset_policy":"NONE","status":"RETIRED"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "55555555-5555-4555-8555-555555555555")
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{
		TenantID: planTestTenantID,
		UserID:   "66666666-6666-4666-8666-666666666666",
	}))
	response := httptest.NewRecorder()

	handler.update(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.updatedTenant != planTestTenantID || store.updatedPlanID != "55555555-5555-4555-8555-555555555555" || store.updatedInput.Status != StatusRetired {
		t.Fatalf("update scope/input = tenant %q plan %q input %+v", store.updatedTenant, store.updatedPlanID, store.updatedInput)
	}
}

func TestRetireUsesDedicatedTenantScopedLifecycleAction(t *testing.T) {
	handler, store := newTestHTTP(t)
	const planID = "55555555-5555-4555-8555-555555555555"
	store.retiredPlan = Plan{ID: planID, Name: "Day Pass", Status: StatusRetired}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plans/"+planID+"/retire", nil)
	request.RemoteAddr = "192.0.2.44:8080"
	request.SetPathValue("id", planID)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{
		TenantID: planTestTenantID,
		UserID:   "66666666-6666-4666-8666-666666666666",
	}))
	response := httptest.NewRecorder()

	handler.retire(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.retiredTenant != planTestTenantID || store.retiredPlanID != planID || store.retiredActor.UserID != "66666666-6666-4666-8666-666666666666" || store.retiredActor.IP != "192.0.2.44" {
		t.Fatalf("retire scope/actor = tenant %q plan %q actor %+v", store.retiredTenant, store.retiredPlanID, store.retiredActor)
	}
	if !strings.Contains(response.Body.String(), `"status":"RETIRED"`) {
		t.Fatalf("retire response = %s", response.Body.String())
	}
}

func TestRestoreUsesDedicatedTenantScopedLifecycleAction(t *testing.T) {
	handler, store := newTestHTTP(t)
	const planID = "55555555-5555-4555-8555-555555555555"
	store.restoredPlan = Plan{ID: planID, Name: "Day Pass", Status: StatusActive}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plans/"+planID+"/restore", nil)
	request.SetPathValue("id", planID)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{
		TenantID: planTestTenantID,
		UserID:   "66666666-6666-4666-8666-666666666666",
	}))
	response := httptest.NewRecorder()

	handler.restore(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.restoredTenant != planTestTenantID || store.restoredPlanID != planID || store.restoredActor.UserID != "66666666-6666-4666-8666-666666666666" {
		t.Fatalf("restore scope/actor = tenant %q plan %q actor %+v", store.restoredTenant, store.restoredPlanID, store.restoredActor)
	}
	if !strings.Contains(response.Body.String(), `"status":"ACTIVE"`) {
		t.Fatalf("restore response = %s", response.Body.String())
	}
}

func TestDeleteRejectsPlansWithSubscriptionOrVoucherHistory(t *testing.T) {
	handler, store := newTestHTTP(t)
	const planID = "55555555-5555-4555-8555-555555555555"
	store.deletedErr = ErrDeleteBlocked
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/plans/"+planID, nil)
	request.SetPathValue("id", planID)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{
		TenantID: planTestTenantID,
		UserID:   "66666666-6666-4666-8666-666666666666",
	}))
	response := httptest.NewRecorder()

	handler.delete(response, request)

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "PLAN_DELETE_BLOCKED") {
		t.Fatalf("delete response = %d %s", response.Code, response.Body.String())
	}
	if store.deletedTenant != planTestTenantID || store.deletedPlanID != planID || store.deletedActor.UserID != "66666666-6666-4666-8666-666666666666" {
		t.Fatalf("delete scope/actor = tenant %q plan %q actor %+v", store.deletedTenant, store.deletedPlanID, store.deletedActor)
	}
}

func TestDeletePristinePlanReturnsNoContent(t *testing.T) {
	handler, store := newTestHTTP(t)
	const planID = "55555555-5555-4555-8555-555555555555"
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/plans/"+planID, nil)
	request.SetPathValue("id", planID)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{
		TenantID: planTestTenantID,
		UserID:   "66666666-6666-4666-8666-666666666666",
	}))
	response := httptest.NewRecorder()

	handler.delete(response, request)

	if response.Code != http.StatusNoContent || response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("delete response = status %d body %q headers %+v", response.Code, response.Body.String(), response.Header())
	}
	if store.deletedTenant != planTestTenantID || store.deletedPlanID != planID {
		t.Fatalf("delete scope = tenant %q plan %q", store.deletedTenant, store.deletedPlanID)
	}
}

func TestCursorRoundTripAndRejectsExtraFields(t *testing.T) {
	cursor := Cursor{
		CreatedAt: time.Date(2026, 8, 12, 10, 0, 0, 123, time.UTC),
		ID:        "44444444-4444-4444-8444-444444444444",
	}
	decoded, err := decodeCursor(encodeCursor(cursor))
	if err != nil || decoded != cursor {
		t.Fatalf("cursor = %+v, %v", decoded, err)
	}
	extra := base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-08-12T10:00:00Z","id":"44444444-4444-4444-8444-444444444444","extra":true}`))
	if _, err := decodeCursor(extra); err == nil {
		t.Fatal("cursor with an unexpected field was accepted")
	}
}
