package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
)

type memoryAccountStore struct {
	tenantID      string
	tenantFound   bool
	resolvedSlug  string
	preparedEmail string
	preparedHash  string
	verifiedEmail string
	resetEmail    string
	resetHash     string
	resolveErr    error
	prepareErr    error
	verifyErr     error
	resetErr      error
}

func (s *memoryAccountStore) ResolveTenant(_ context.Context, slug string) (string, bool, error) {
	s.resolvedSlug = slug
	if slug != "example" {
		return "", false, nil
	}
	return s.tenantID, s.tenantFound, s.resolveErr
}

func newTestAccountHTTP(t *testing.T) (*AccountHTTP, *memoryAccountStore, *recordingNotifier) {
	t.Helper()
	service, store, notifier := newTestAccountService(t)
	loginService, loginStore := newTestService(t)
	loginStore.user.RequiresMFA = false
	loginStore.user.EmailVerified = true
	handler, err := NewAccountHTTP(service, loginService, &testLimiter{allowed: true}, "example", false, []string{"https://portal.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, notifier
}

func (s *memoryAccountStore) PrepareEmailRegistration(_ context.Context, tenantID, email, passwordHash string) error {
	if tenantID != s.tenantID {
		return errors.New("unexpected tenant")
	}
	s.preparedEmail, s.preparedHash = email, passwordHash
	return s.prepareErr
}

func (s *memoryAccountStore) VerifyEmailAndEnsureCustomer(_ context.Context, tenantID, email string) error {
	if tenantID != s.tenantID {
		return errors.New("unexpected tenant")
	}
	s.verifiedEmail = email
	return s.verifyErr
}

func (s *memoryAccountStore) ResetVerifiedPassword(_ context.Context, tenantID, email, passwordHash string) error {
	if tenantID != s.tenantID {
		return errors.New("unexpected tenant")
	}
	s.resetEmail, s.resetHash = email, passwordHash
	return s.resetErr
}

func newTestAccountService(t *testing.T) (*AccountService, *memoryAccountStore, *recordingNotifier) {
	t.Helper()
	hasher, err := argon2id.New(argon2id.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryAccountStore{tenantID: testTenantID, tenantFound: true}
	notifier := &recordingNotifier{}
	otp, err := NewOTPService(newMemoryOTPStore(), notifier)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewAccountService(store, hasher, otp)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, notifier
}

func TestAccountServiceVerifiesEmailBeforeCreatingCustomer(t *testing.T) {
	service, store, notifier := newTestAccountService(t)
	service.now = func() time.Time { return time.Date(2026, 8, 24, 18, 0, 0, 0, time.UTC) }

	issued, err := service.BeginRegistration(context.Background(), RegistrationInput{
		TenantSlug: "example", Email: "Customer@Example.com", Password: "correct customer password",
	})
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	if store.preparedEmail != "customer@example.com" || store.preparedHash == "" || store.verifiedEmail != "" {
		t.Fatalf("registration state prepared_email=%q prepared_hash=%q verified=%q", store.preparedEmail, store.preparedHash, store.verifiedEmail)
	}
	if issued.ChallengeID == "" || notifier.code == "" {
		t.Fatalf("verification issuance = %+v code=%q", issued, notifier.code)
	}

	err = service.VerifyRegistration(context.Background(), EmailVerificationInput{
		TenantSlug: "example", Email: "customer@example.com", ChallengeID: issued.ChallengeID, Code: notifier.code,
	})
	if err != nil {
		t.Fatalf("VerifyRegistration: %v", err)
	}
	if store.verifiedEmail != "customer@example.com" {
		t.Fatalf("verified email = %q", store.verifiedEmail)
	}
}

func TestVerifyEmailLinksExistingUnlinkedCustomer(t *testing.T) {
	action, err := selectCustomerLinkAction(nil, pgtype.Text{}, "33333333-3333-4333-8333-333333333333")
	if err != nil || action != linkExistingCustomer {
		t.Fatalf("action=%v err=%v, want link existing customer", action, err)
	}
}

func TestVerifyEmailPreservesSameCustomerLinkOnRetry(t *testing.T) {
	userID := "33333333-3333-4333-8333-333333333333"
	action, err := selectCustomerLinkAction(nil, pgtype.Text{String: userID, Valid: true}, userID)
	if err != nil || action != preserveExistingCustomer {
		t.Fatalf("action=%v err=%v, want preserve existing customer", action, err)
	}
}

func TestVerifyEmailCustomerLinkQueriesAreTenantScopedAndAtomic(t *testing.T) {
	if !strings.Contains(customerProfileForUpdateSQL, "WHERE tenant_id = $1 AND email = $2") || !strings.Contains(customerProfileForUpdateSQL, "FOR UPDATE") {
		t.Fatalf("profile lookup must lock the canonical tenant match: %s", customerProfileForUpdateSQL)
	}
	if !strings.Contains(linkExistingCustomerSQL, "WHERE tenant_id = $1 AND id = $2::uuid AND user_id IS NULL") || !strings.Contains(linkExistingCustomerSQL, "SET user_id = $3::uuid") {
		t.Fatalf("unlinked profile update is not guarded: %s", linkExistingCustomerSQL)
	}
	if !strings.Contains(createDefaultCustomerSQL, "ON CONFLICT (tenant_id, user_id)") || strings.Contains(createDefaultCustomerSQL, "ON CONFLICT (tenant_id, email)") {
		t.Fatalf("default profile fallback is not idempotent: %s", createDefaultCustomerSQL)
	}
}

func TestVerifyEmailAndEnsureCustomerTxLinksExistingOrCreatesOnlyWhenMissing(t *testing.T) {
	const userID = "33333333-3333-4333-8333-333333333333"
	tests := []struct {
		name        string
		rows        []pgx.Row
		wantLink    bool
		wantDefault bool
		wantAudit   bool
	}{
		{
			name: "links unlinked profile",
			rows: []pgx.Row{
				customerLinkRow{values: []any{userID}},
				customerLinkRow{values: []any{"44444444-4444-4444-8444-444444444444", pgtype.Text{}}},
			},
			wantLink: true, wantAudit: true,
		},
		{
			name: "preserves matching profile link",
			rows: []pgx.Row{
				customerLinkRow{values: []any{userID}},
				customerLinkRow{values: []any{"44444444-4444-4444-8444-444444444444", pgtype.Text{String: userID, Valid: true}}},
			},
			wantAudit: true,
		},
		{
			name: "creates default only when no profile matches",
			rows: []pgx.Row{
				customerLinkRow{values: []any{userID}},
				customerLinkRow{err: pgx.ErrNoRows},
				customerLinkRow{values: []any{"55555555-5555-4555-8555-555555555555"}},
			},
			wantDefault: true, wantAudit: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := &customerLinkTx{rows: test.rows}
			if err := verifyEmailAndEnsureCustomerTx(context.Background(), tx, testTenantID, "customer@example.com"); err != nil {
				t.Fatal(err)
			}
			if len(tx.queries) < 2 || tx.queries[1] != customerProfileForUpdateSQL {
				t.Fatalf("customer profile was not selected with the production lock query: %v", tx.queries)
			}
			if got := containsSQL(tx.execs, linkExistingCustomerSQL); got != test.wantLink {
				t.Fatalf("link existing query called=%t want=%t execs=%v", got, test.wantLink, tx.execs)
			}
			if got := containsSQL(tx.queries, createDefaultCustomerSQL); got != test.wantDefault {
				t.Fatalf("default profile query called=%t want=%t queries=%v", got, test.wantDefault, tx.queries)
			}
			if got := containsSQL(tx.execs, "INSERT INTO audit_logs"); got != test.wantAudit {
				t.Fatalf("verification audit called=%t want=%t execs=%v", got, test.wantAudit, tx.execs)
			}
		})
	}
}

type customerLinkTx struct {
	pgx.Tx
	rows    []pgx.Row
	queries []string
	execs   []string
}

func (tx *customerLinkTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	tx.queries = append(tx.queries, query)
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return row
}

func (tx *customerLinkTx) Exec(_ context.Context, query string, _ ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, query)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

type customerLinkRow struct {
	values []any
	err    error
}

func (row customerLinkRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	for index, destination := range destinations {
		switch destination := destination.(type) {
		case *string:
			*destination = row.values[index].(string)
		case *pgtype.Text:
			*destination = row.values[index].(pgtype.Text)
		default:
			panic("unexpected scan destination")
		}
	}
	return nil
}

func containsSQL(queries []string, want string) bool {
	for _, query := range queries {
		if query == want || strings.Contains(query, want) {
			return true
		}
	}
	return false
}

func TestAccountServiceDoesNotVerifyEmailWhenCodeIsWrong(t *testing.T) {
	service, store, _ := newTestAccountService(t)
	issued, err := service.BeginRegistration(context.Background(), RegistrationInput{
		TenantSlug: "example", Email: "customer@example.com", Password: "correct customer password",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = service.VerifyRegistration(context.Background(), EmailVerificationInput{
		TenantSlug: "example", Email: "customer@example.com", ChallengeID: issued.ChallengeID, Code: "000000",
	})
	if !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("wrong code error = %v, want ErrInvalidOTP", err)
	}
	if store.verifiedEmail != "" {
		t.Fatalf("wrong code verified %q", store.verifiedEmail)
	}
}

func TestAccountServicePasswordResetRequiresAValidEmailBoundCode(t *testing.T) {
	service, store, notifier := newTestAccountService(t)
	issued, err := service.RequestPasswordReset(context.Background(), "example", "customer@example.com")
	if err != nil {
		t.Fatal(err)
	}
	err = service.ConfirmPasswordReset(context.Background(), PasswordResetInput{
		TenantSlug: "example", Email: "other@example.com", ChallengeID: issued.ChallengeID, Code: notifier.code, Password: "a new customer password",
	})
	if !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("wrong email reset error = %v, want ErrInvalidOTP", err)
	}
	if store.resetHash != "" {
		t.Fatal("reset occurred for a code bound to a different email")
	}
}

func TestAccountHTTPRegistersAgainstItsConfiguredTenant(t *testing.T) {
	handler, store, _ := newTestAccountHTTP(t)
	mux := http.NewServeMux()
	handler.Routes(mux)

	request := httptest.NewRequest(http.MethodPost, "/portal/auth/register", strings.NewReader(`{"email":"customer@example.com","password":"correct customer password"}`))
	request.RemoteAddr = "203.0.113.9:4040"
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.resolvedSlug != "example" || store.preparedEmail != "customer@example.com" {
		t.Fatalf("trusted tenant=%q prepared email=%q", store.resolvedSlug, store.preparedEmail)
	}
	if strings.Contains(response.Body.String(), "tenant") || !strings.Contains(response.Body.String(), "challenge_id") {
		t.Fatalf("public registration response=%s", response.Body)
	}
}

func TestAccountHTTPRejectsBrowserTenantSelector(t *testing.T) {
	handler, store, _ := newTestAccountHTTP(t)
	mux := http.NewServeMux()
	handler.Routes(mux)

	request := httptest.NewRequest(http.MethodPost, "/portal/auth/register", strings.NewReader(`{"tenant":"attacker","email":"customer@example.com","password":"correct customer password"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || store.preparedEmail != "" {
		t.Fatalf("status=%d prepared=%q body=%s", response.Code, store.preparedEmail, response.Body)
	}
}

func TestAccountHTTPDoesNotExposeCustomerLinkConflict(t *testing.T) {
	handler, store, notifier := newTestAccountHTTP(t)
	issued, err := handler.service.BeginRegistration(context.Background(), RegistrationInput{
		TenantSlug: "example", Email: "customer@example.com", Password: "correct customer password",
	})
	if err != nil {
		t.Fatal(err)
	}
	store.verifyErr = errors.New("customer profile is already linked to another user")
	mux := http.NewServeMux()
	handler.Routes(mux)
	request := httptest.NewRequest(http.MethodPost, "/portal/auth/verify-email", strings.NewReader(`{"email":"customer@example.com","challenge_id":"`+issued.ChallengeID+`","code":"`+notifier.code+`"}`))
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "AUTH_UNAVAILABLE") || strings.Contains(response.Body.String(), "linked to another user") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestAccountHTTPPasswordResetRequestHasGenericResponse(t *testing.T) {
	handler, _, _ := newTestAccountHTTP(t)
	mux := http.NewServeMux()
	handler.Routes(mux)

	request := func(email string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/portal/auth/password-reset/request", strings.NewReader(`{"email":"`+email+`"}`))
		req.RemoteAddr = "203.0.113.9:4040"
		mux.ServeHTTP(response, req)
		return response
	}
	known := request("customer@example.com")
	unknown := request("unknown@example.com")
	if known.Code != http.StatusAccepted || unknown.Code != http.StatusAccepted || !strings.Contains(known.Body.String(), "challenge_id") || !strings.Contains(unknown.Body.String(), "challenge_id") {
		t.Fatalf("reset responses known=%d/%s unknown=%d/%s", known.Code, known.Body, unknown.Code, unknown.Body)
	}
}

func TestAccountHTTPPortalLoginUsesConfiguredTenant(t *testing.T) {
	handler, _, _ := newTestAccountHTTP(t)
	mux := http.NewServeMux()
	handler.Routes(mux)
	request := httptest.NewRequest(http.MethodPost, "/portal/auth/login", strings.NewReader(`{"identifier":"admin@example.com","password":"correct password"}`))
	request.RemoteAddr = "203.0.113.9:4040"
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 || strings.Contains(response.Body.String(), "tenant") {
		t.Fatalf("status=%d cookies=%v body=%s", response.Code, response.Result().Cookies(), response.Body)
	}
}
