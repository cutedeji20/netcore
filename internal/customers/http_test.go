package customers

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
	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
)

const customerTestTenantID = "11111111-1111-4111-8111-111111111111"

type memoryStore struct {
	tenantID string
	options  ListOptions
	page     Page
	err      error

	createdTenant string
	createdActor  MutationActor
	createdInput  WriteInput
	created       Customer

	updatedTenant string
	updatedID     string
	updatedActor  MutationActor
	updatedInput  WriteInput
	updated       Customer

	deactivatedTenant string
	deactivatedID     string
	deactivatedActor  MutationActor
	deactivated       Customer
	mutationCalls     int
}

func (s *memoryStore) List(_ context.Context, tenantID string, options ListOptions) (Page, error) {
	s.tenantID = tenantID
	s.options = options
	return s.page, s.err
}

func (s *memoryStore) Create(_ context.Context, tenantID string, actor MutationActor, input WriteInput) (Customer, error) {
	s.mutationCalls++
	s.createdTenant, s.createdActor, s.createdInput = tenantID, actor, input
	return s.created, s.err
}

func (s *memoryStore) Update(_ context.Context, tenantID, customerID string, actor MutationActor, input WriteInput) (Customer, error) {
	s.mutationCalls++
	s.updatedTenant, s.updatedID, s.updatedActor, s.updatedInput = tenantID, customerID, actor, input
	return s.updated, s.err
}

func (s *memoryStore) Deactivate(_ context.Context, tenantID, customerID string, actor MutationActor) (Customer, error) {
	s.mutationCalls++
	s.deactivatedTenant, s.deactivatedID, s.deactivatedActor = tenantID, customerID, actor
	return s.deactivated, s.err
}

func newTestHTTP(t *testing.T) (*HTTP, *memoryStore) {
	t.Helper()
	store := &memoryStore{
		page: Page{
			Customers: []Customer{{
				ID:             "22222222-2222-4222-8222-222222222222",
				CustomerNumber: "CUS-10482",
				Status:         "ACTIVE",
				FirstName:      "Chika",
				LastName:       "Nwosu",
				Phone:          "+2348031114280",
				Email:          "chika@example.test",
				CreatedAt:      time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
				UpdatedAt:      time.Date(2026, 8, 12, 10, 5, 0, 0, time.UTC),
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
	request := httptest.NewRequest(http.MethodGet, "/api/v1/customers?limit=10&q=chika&cursor="+cursor, nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: customerTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if store.tenantID != customerTestTenantID || store.options.Limit != 10 || store.options.Search != "chika" || store.options.Cursor.ID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("unexpected store request: tenant=%q options=%+v", store.tenantID, store.options)
	}
	var body listResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].Email != "chika@example.test" || body.Data[0].ID == "" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestListRejectsUnsafePageParameters(t *testing.T) {
	handler, _ := newTestHTTP(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/customers?limit=101", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: customerTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestListRejectsMissingPrincipal(t *testing.T) {
	handler, _ := newTestHTTP(t)
	response := httptest.NewRecorder()

	handler.list(response, httptest.NewRequest(http.MethodGet, "/api/v1/customers", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
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

func TestCreateUsesTenantActorAndCanonicalProfileInput(t *testing.T) {
	handler, store := newTestHTTP(t)
	store.created = store.page.Customers[0]
	request := httptest.NewRequest(http.MethodPost, "/api/v1/customers", strings.NewReader(`{"first_name":" Chika ","last_name":" Nwosu ","email":" CHIKA@Example.test ","phone":" +2348031114280 "}`))
	request.Header.Set("User-Agent", "NetCore test operator")
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: customerTestTenantID, UserID: "66666666-6666-4666-8666-666666666666"}))
	response := httptest.NewRecorder()

	handler.create(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.createdTenant != customerTestTenantID || store.createdActor.UserID == "" || store.createdInput.Email != "chika@example.test" {
		t.Fatalf("create scope/input tenant=%q actor=%+v input=%+v", store.createdTenant, store.createdActor, store.createdInput)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("customer write response must not be cacheable")
	}
}

func TestUpdateAndDeactivateHideCrossTenantTargets(t *testing.T) {
	handler, store := newTestHTTP(t)
	store.err = ErrNotFound
	principal := auth.Principal{TenantID: customerTestTenantID, UserID: "66666666-6666-4666-8666-666666666666"}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPut, "/api/v1/customers/55555555-5555-4555-8555-555555555555", strings.NewReader(`{"first_name":"Chika","last_name":"Nwosu","email":"chika@example.test"}`)),
		httptest.NewRequest(http.MethodPost, "/api/v1/customers/55555555-5555-4555-8555-555555555555/deactivate", nil),
	} {
		request.SetPathValue("id", "55555555-5555-4555-8555-555555555555")
		request = request.WithContext(auth.ContextWithPrincipal(request.Context(), principal))
		response := httptest.NewRecorder()
		if request.Method == http.MethodPut {
			handler.update(response, request)
		} else {
			handler.deactivate(response, request)
		}
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", request.Method, response.Code, response.Body)
		}
	}
}

func TestCreateReturnsSafeDuplicateConflict(t *testing.T) {
	handler, store := newTestHTTP(t)
	store.err = ErrDuplicateEmail
	request := httptest.NewRequest(http.MethodPost, "/api/v1/customers", strings.NewReader(`{"first_name":"Chika","last_name":"Nwosu","email":"chika@example.test"}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: customerTestTenantID, UserID: "66666666-6666-4666-8666-666666666666"}))
	response := httptest.NewRecorder()

	handler.create(response, request)

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "CUSTOMER_EMAIL_EXISTS") || strings.Contains(response.Body.String(), "duplicate key") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestMutationRoutesRequireCustomerWriteAndAnAllowedOrigin(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/customers", body: `{"first_name":"Chika","last_name":"Nwosu","email":"chika@example.test"}`},
		{name: "update", method: http.MethodPut, path: "/api/v1/customers/55555555-5555-4555-8555-555555555555", body: `{"first_name":"Chika","last_name":"Nwosu","email":"chika@example.test"}`},
		{name: "deactivate", method: http.MethodPost, path: "/api/v1/customers/55555555-5555-4555-8555-555555555555/deactivate"},
	}
	for _, test := range tests {
		t.Run(test.name+" denies missing customer.write", func(t *testing.T) {
			mux, store, token := newCustomerMutationRouteMux(t, map[string]struct{}{"customer.read": {}})
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Origin", "https://dashboard.example.test")
			request.AddCookie(&http.Cookie{Name: "netcore_session", Value: token})
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || store.mutationCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, store.mutationCalls, response.Body)
			}
		})
		t.Run(test.name+" denies cross origin", func(t *testing.T) {
			mux, store, token := newCustomerMutationRouteMux(t, map[string]struct{}{"customer.write": {}})
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Origin", "https://attacker.example.test")
			request.AddCookie(&http.Cookie{Name: "netcore_session", Value: token})
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || store.mutationCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, store.mutationCalls, response.Body)
			}
		})
	}
}

type customerRouteAuthStore struct {
	user        auth.User
	permissions map[string]struct{}
	tokenHash   []byte
}

func (s *customerRouteAuthStore) ResolveTenant(_ context.Context, slug string) (string, bool, error) {
	return customerTestTenantID, slug == "example", nil
}

func (s *customerRouteAuthStore) FindUser(_ context.Context, tenantID, identifier string) (auth.User, bool, error) {
	return s.user, tenantID == customerTestTenantID && identifier == s.user.Email, nil
}

func (s *customerRouteAuthStore) CreateSession(_ context.Context, _ auth.User, tokenHash []byte, _ time.Time, _, _ string) error {
	s.tokenHash = append([]byte(nil), tokenHash...)
	return nil
}

func (s *customerRouteAuthStore) SessionPrincipal(_ context.Context, tenantID string, tokenHash []byte) (auth.Principal, bool, error) {
	if tenantID != customerTestTenantID || string(tokenHash) != string(s.tokenHash) {
		return auth.Principal{}, false, nil
	}
	now := time.Now().UTC()
	return auth.Principal{TenantID: tenantID, UserID: s.user.ID, Email: s.user.Email, SessionCreatedAt: now, SessionExpiresAt: now.Add(time.Hour), Permissions: s.permissions}, true, nil
}

func (s *customerRouteAuthStore) InvalidateSession(context.Context, string, []byte) error { return nil }
func (s *customerRouteAuthStore) UpdatePasswordHash(context.Context, string, string, string) error {
	return nil
}

type customerRouteLimiter struct{}

func (customerRouteLimiter) AllowSlidingWindow(context.Context, string, int64, time.Duration) (bool, error) {
	return true, nil
}

func newCustomerMutationRouteMux(t *testing.T, permissions map[string]struct{}) (*http.ServeMux, *memoryStore, string) {
	t.Helper()
	hasher, err := argon2id.New(argon2id.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hasher.Hash("correct password")
	if err != nil {
		t.Fatal(err)
	}
	store := &customerRouteAuthStore{user: auth.User{ID: "66666666-6666-4666-8666-666666666666", TenantID: customerTestTenantID, Email: "operator@example.test", PasswordHash: hash, Status: "ACTIVE", EmailVerified: true}, permissions: permissions}
	service, err := auth.NewService(store, hasher, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := auth.NewHTTP(service, customerRouteLimiter{}, false, []string{"https://dashboard.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, customerStore := newTestHTTP(t)
	mux := http.NewServeMux()
	if err := handler.Routes(mux, sessions); err != nil {
		t.Fatal(err)
	}
	session, _, err := service.Login(context.Background(), auth.LoginInput{TenantSlug: "example", Identifier: "operator@example.test", Password: "correct password"})
	if err != nil {
		t.Fatal(err)
	}
	return mux, customerStore, session.Token
}
