package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// env builds a getenv func from a map, so tests never touch process state.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func baseProd() map[string]string {
	return map[string]string{
		"NETCORE_ENV":              "production",
		"NETCORE_DB_DSN":           "postgres://u:p@db:5432/netcore?sslmode=require",
		"NETCORE_REDIS_ADDR":       "redis:6379",
		"NETCORE_REDIS_PASSWORD":   "test-redis-password",
		"NETCORE_ALLOWED_ORIGINS":  "https://portal.example.com",
		"NETCORE_TRUSTED_PROXIES":  "172.30.0.2",
		"NETCORE_SECRETS_REF":      "netcore/prod",
		"NETCORE_AUTH_REQUIRE_MFA": "true",
	}
}

func TestLoad_DevelopmentDefaults(t *testing.T) {
	c, err := Load(env(map[string]string{"NETCORE_DB_DSN": "postgres://localhost/dev"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Env != EnvDevelopment {
		t.Errorf("Env = %q, want development", c.Env)
	}
	if c.Quota.InterimIntervalBase != 300*time.Second {
		t.Errorf("interim base = %v, want 300s (§21A.5)", c.Quota.InterimIntervalBase)
	}
	if c.Limits.MaxWebhookBytes != 64*1024 {
		t.Errorf("webhook limit = %d, want 65536 (§16)", c.Limits.MaxWebhookBytes)
	}
}

func TestLoad_MissingDSNFails(t *testing.T) {
	if _, err := Load(env(map[string]string{})); err == nil {
		t.Fatal("expected failure with no DSN")
	}
}

// §105 — the production safety invariants. Each of these is a real incident
// that a presence-only check would have allowed to boot.
func TestValidate_ProductionSafetyInvariants(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]string)
		wantSub string
	}{
		{
			name:    "CORS wildcard is rejected",
			mutate:  func(m map[string]string) { m["NETCORE_ALLOWED_ORIGINS"] = "*" },
			wantSub: "must not contain '*'",
		},
		{
			name:    "empty CORS list is rejected",
			mutate:  func(m map[string]string) { delete(m, "NETCORE_ALLOWED_ORIGINS") },
			wantSub: "NETCORE_ALLOWED_ORIGINS is required",
		},
		{
			name:    "plaintext http origin is rejected",
			mutate:  func(m map[string]string) { m["NETCORE_ALLOWED_ORIGINS"] = "http://portal.example.com" },
			wantSub: "must use https",
		},
		{
			name:    "TLS verification cannot be disabled",
			mutate:  func(m map[string]string) { m["NETCORE_TLS_SKIP_VERIFY"] = "true" },
			wantSub: "TLS_SKIP_VERIFY must be false",
		},
		{
			name:    "seed data cannot be enabled",
			mutate:  func(m map[string]string) { m["NETCORE_SEED_ENABLED"] = "true" },
			wantSub: "SEED_ENABLED must be false",
		},
		{
			name:    "database TLS cannot be disabled",
			mutate:  func(m map[string]string) { m["NETCORE_DB_DSN"] = "postgres://u:p@db/n?sslmode=disable" },
			wantSub: "must not disable TLS",
		},
		{
			name:    "redis is required for rate limiting",
			mutate:  func(m map[string]string) { delete(m, "NETCORE_REDIS_ADDR") },
			wantSub: "NETCORE_REDIS_ADDR is required",
		},
		{
			name:    "redis password is required for rate limiting",
			mutate:  func(m map[string]string) { delete(m, "NETCORE_REDIS_PASSWORD") },
			wantSub: "NETCORE_REDIS_PASSWORD",
		},
		{
			name:    "trusted proxy is required behind the production edge",
			mutate:  func(m map[string]string) { delete(m, "NETCORE_TRUSTED_PROXIES") },
			wantSub: "NETCORE_TRUSTED_PROXIES is required",
		},
		{
			name:    "secret reference is required",
			mutate:  func(m map[string]string) { delete(m, "NETCORE_SECRETS_REF") },
			wantSub: "NETCORE_SECRETS_REF is required",
		},
		{
			name:    "MFA is required for privileged production access",
			mutate:  func(m map[string]string) { m["NETCORE_AUTH_REQUIRE_MFA"] = "false" },
			wantSub: "NETCORE_AUTH_REQUIRE_MFA must be true",
		},
		{
			// §82.3: Session-Timeout is the outage exposure window.
			name:    "session timeout beyond 24h is rejected",
			mutate:  func(m map[string]string) { m["NETCORE_SESSION_TIMEOUT"] = "72h" },
			wantSub: "outage exposure window",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := baseProd()
			c.mutate(m)
			_, err := Load(env(m))
			if err == nil {
				t.Fatalf("expected rejection, config loaded successfully")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("error %q does not mention %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestValidate_ValidProductionConfigLoads(t *testing.T) {
	c, err := Load(env(baseProd()))
	if err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
	if !c.Env.IsProduction() {
		t.Error("expected production env")
	}
	// §48: Redis must never gate readiness, regardless of environment.
	if c.Redis.RequiredForReadiness {
		t.Error("Redis must not be a readiness gate (§48)")
	}
}

func TestLoad_SensitiveValuesMayComeFromMountedFiles(t *testing.T) {
	dir := t.TempDir()
	dsnFile := filepath.Join(dir, "db_dsn")
	redisFile := filepath.Join(dir, "redis_password")
	if err := os.WriteFile(dsnFile, []byte("postgres://u:p@db:5432/netcore?sslmode=require\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(redisFile, []byte("test-redis-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := baseProd()
	delete(m, "NETCORE_DB_DSN")
	delete(m, "NETCORE_REDIS_PASSWORD")
	m["NETCORE_DB_DSN_FILE"] = dsnFile
	m["NETCORE_REDIS_PASSWORD_FILE"] = redisFile
	c, err := Load(env(m))
	if err != nil {
		t.Fatalf("mounted secret config rejected: %v", err)
	}
	if c.Database.DSN != "postgres://u:p@db:5432/netcore?sslmode=require" || c.Redis.Password != "test-redis-password" {
		t.Fatalf("mounted values were not loaded safely: %#v", c)
	}
}

func TestLoad_SensitiveValueAndFileAreMutuallyExclusive(t *testing.T) {
	m := baseProd()
	m["NETCORE_DB_DSN_FILE"] = "/run/secrets/db_dsn"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "cannot both be set") {
		t.Fatalf("ambiguous secret source must fail, err=%v", err)
	}
}

// Development is permissive by design — these must NOT fail outside production,
// or nobody can run the stack locally.
func TestValidate_DevelopmentAllowsLooseSettings(t *testing.T) {
	m := map[string]string{
		"NETCORE_ENV":             "development",
		"NETCORE_DB_DSN":          "postgres://localhost/dev?sslmode=disable",
		"NETCORE_ALLOWED_ORIGINS": "*",
		"NETCORE_SEED_ENABLED":    "true",
	}
	if _, err := Load(env(m)); err != nil {
		t.Fatalf("development config should load: %v", err)
	}
}

// §21A.5 — the accounting-flood guard applies in every environment.
func TestValidate_QuotaIntervalFloorAppliesEverywhere(t *testing.T) {
	m := map[string]string{
		"NETCORE_ENV":                "development",
		"NETCORE_DB_DSN":             "postgres://localhost/dev",
		"NETCORE_QUOTA_INTERIM_BASE": "30s",
	}
	_, err := Load(env(m))
	if err == nil {
		t.Fatal("expected sub-60s interim interval to be rejected")
	}
	if !strings.Contains(err.Error(), "§21A.5") {
		t.Fatalf("error should cite §21A.5: %v", err)
	}
}

// v1.2 §21A.3 — a zero floor reintroduces the budget asymptote.
func TestValidate_QuotaMinPerSessionMustBeNonZero(t *testing.T) {
	m := map[string]string{
		"NETCORE_ENV":                     "development",
		"NETCORE_DB_DSN":                  "postgres://localhost/dev",
		"NETCORE_QUOTA_MIN_SESSION_BYTES": "0",
	}
	_, err := Load(env(m))
	if err == nil {
		t.Fatal("expected zero minimum session budget to be rejected")
	}
	if !strings.Contains(err.Error(), "asymptote") {
		t.Fatalf("error should explain the asymptote: %v", err)
	}
}

func TestValidate_FineIntervalCannotExceedBase(t *testing.T) {
	m := map[string]string{
		"NETCORE_ENV":                "development",
		"NETCORE_DB_DSN":             "postgres://localhost/dev",
		"NETCORE_QUOTA_INTERIM_BASE": "60s",
		"NETCORE_QUOTA_INTERIM_FINE": "300s",
	}
	if _, err := Load(env(m)); err == nil {
		t.Fatal("expected fine > base to be rejected")
	}
}

func TestValidate_DependencyTimeoutsMustBePositive(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "database connect timeout", key: "NETCORE_DB_CONNECT_TIMEOUT"},
		{name: "redis timeout", key: "NETCORE_REDIS_TIMEOUT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]string{
				"NETCORE_ENV":    "development",
				"NETCORE_DB_DSN": "postgres://localhost/dev",
				tt.key:           "0s",
			}
			if _, err := Load(env(m)); err == nil {
				t.Fatalf("expected %s=0s to be rejected", tt.key)
			}
		})
	}
}

func TestValidate_AuthPolicyRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "session TTL", key: "NETCORE_AUTH_SESSION_TTL", value: "0s"},
		{name: "password memory", key: "NETCORE_AUTH_PASSWORD_MEMORY_KIB", value: "1"},
		{name: "password iterations", key: "NETCORE_AUTH_PASSWORD_ITERATIONS", value: "0"},
		{name: "password parallelism", key: "NETCORE_AUTH_PASSWORD_PARALLELISM", value: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]string{
				"NETCORE_ENV":    "development",
				"NETCORE_DB_DSN": "postgres://localhost/dev",
				tt.key:           tt.value,
			}
			if _, err := Load(env(m)); err == nil {
				t.Fatalf("expected %s=%s to be rejected", tt.key, tt.value)
			}
		})
	}
}

func TestValidate_UnknownEnvRejected(t *testing.T) {
	m := map[string]string{"NETCORE_ENV": "prod", "NETCORE_DB_DSN": "postgres://x/y"}
	if _, err := Load(env(m)); err == nil {
		t.Fatal("expected unknown env to be rejected (prod != production)")
	}
}

func TestValidate_UnknownSecretsBackendRejected(t *testing.T) {
	m := map[string]string{
		"NETCORE_ENV":             "development",
		"NETCORE_DB_DSN":          "postgres://localhost/dev",
		"NETCORE_SECRETS_BACKEND": "env",
	}
	if _, err := Load(env(m)); err == nil {
		t.Fatal("expected unknown secrets backend to be rejected (§68)")
	}
}

func TestValidate_PaystackNeedsLogicalSecretReference(t *testing.T) {
	m := map[string]string{
		"NETCORE_ENV":             "development",
		"NETCORE_DB_DSN":          "postgres://localhost/dev",
		"NETCORE_PAYMENT_GATEWAY": "paystack",
		"NETCORE_SECRETS_REF":     "/run/secrets/netcore.json",
		"NETCORE_ALLOWED_ORIGINS": "https://portal.example.test",
	}
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "PAYSTACK_SECRET_REF") {
		t.Fatalf("expected missing Paystack secret reference to fail, err=%v", err)
	}
	m["NETCORE_PAYSTACK_SECRET_REF"] = "payments.paystack.secret_key"
	m["NETCORE_PAYSTACK_CALLBACK_URL"] = "https://portal.example.test/portal.html"
	if _, err := Load(env(m)); err != nil {
		t.Fatalf("configured Paystack gateway rejected: %v", err)
	}
}

func TestValidate_PaystackRequiresTrustedHTTPSCallbackURL(t *testing.T) {
	m := baseProd()
	m["NETCORE_PAYMENT_GATEWAY"] = "paystack"
	m["NETCORE_PAYSTACK_SECRET_REF"] = "payments.paystack.secret_key"
	m["NETCORE_PAYSTACK_CALLBACK_URL"] = "http://portal.example.com/portal.html"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "PAYSTACK_CALLBACK_URL") {
		t.Fatalf("insecure Paystack callback was accepted: %v", err)
	}
	m["NETCORE_PAYSTACK_CALLBACK_URL"] = "https://other.example.com/portal.html"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "NETCORE_ALLOWED_ORIGINS") {
		t.Fatalf("untrusted Paystack callback was accepted: %v", err)
	}
	m["NETCORE_PAYSTACK_CALLBACK_URL"] = "https://portal.example.com/portal.html"
	if _, err := Load(env(m)); err != nil {
		t.Fatalf("trusted Paystack callback rejected: %v", err)
	}
}

func TestValidate_PaymentGatewayAndWebhookBounds(t *testing.T) {
	for _, tc := range []struct {
		key, value, want string
	}{
		{"NETCORE_PAYMENT_GATEWAY", "other", "must be disabled or paystack"},
		{"NETCORE_WEBHOOK_POLL_INTERVAL", "10ms", "POLL_INTERVAL"},
		{"NETCORE_WEBHOOK_MAX_ATTEMPTS", "0", "MAX_ATTEMPTS"},
		{"NETCORE_MAX_WEBHOOK_BYTES", "70000", "65536"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			m := map[string]string{"NETCORE_ENV": "development", "NETCORE_DB_DSN": "postgres://localhost/dev", tc.key: tc.value}
			if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s=%s to fail with %q, err=%v", tc.key, tc.value, tc.want, err)
			}
		})
	}
}

func TestValidate_ResendEmailProviderRequiresLogicalSecretAndSender(t *testing.T) {
	m := map[string]string{
		"NETCORE_ENV":            "development",
		"NETCORE_DB_DSN":         "postgres://localhost/dev",
		"NETCORE_EMAIL_PROVIDER": "resend",
		"NETCORE_SECRETS_REF":    "/run/secrets/netcore.json",
	}
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "NETCORE_RESEND_API_KEY_REF") {
		t.Fatalf("missing Resend key reference error=%v", err)
	}
	m["NETCORE_RESEND_API_KEY_REF"] = "email.resend.api_key"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "NETCORE_EMAIL_FROM") {
		t.Fatalf("missing Resend sender error=%v", err)
	}
	m["NETCORE_EMAIL_FROM"] = "NetCore <access@notify.durabledatahubs.com>"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "NETCORE_PORTAL_TENANT_SLUG") {
		t.Fatalf("missing trusted portal tenant error=%v", err)
	}
	m["NETCORE_PORTAL_TENANT_SLUG"] = "data-hub"
	config, err := Load(env(m))
	if err != nil {
		t.Fatalf("valid Resend email configuration rejected: %v", err)
	}
	if config.Email.Provider != "resend" || config.Email.ResendAPIKeyRef != "email.resend.api_key" || config.Portal.TenantSlug != "data-hub" {
		t.Fatalf("email/portal configuration = %#v / %#v", config.Email, config.Portal)
	}
	m["NETCORE_EMAIL_FROM"] = "not a sender"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "NETCORE_EMAIL_FROM") {
		t.Fatalf("invalid Resend sender error=%v", err)
	}
	m["NETCORE_EMAIL_FROM"] = "NetCore <access@notify.durabledatahubs.com>"
	m["NETCORE_PORTAL_TENANT_SLUG"] = "DataHub"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "NETCORE_PORTAL_TENANT_SLUG") {
		t.Fatalf("invalid trusted portal tenant error=%v", err)
	}
}

func TestValidate_OptionalPortalTenantSlugMustBeSafe(t *testing.T) {
	m := map[string]string{
		"NETCORE_ENV":                "development",
		"NETCORE_DB_DSN":             "postgres://localhost/dev",
		"NETCORE_PORTAL_TENANT_SLUG": "not a valid slug",
	}
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "NETCORE_PORTAL_TENANT_SLUG") {
		t.Fatalf("invalid optional portal tenant error=%v", err)
	}
}

// All problems must be reported at once. Fixing one per restart is a bad
// operator experience during an incident.
func TestValidationError_ReportsAllProblems(t *testing.T) {
	m := baseProd()
	m["NETCORE_ALLOWED_ORIGINS"] = "*"
	m["NETCORE_TLS_SKIP_VERIFY"] = "true"
	m["NETCORE_SEED_ENABLED"] = "true"

	_, err := Load(env(m))
	if err == nil {
		t.Fatal("expected failure")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if len(ve.Problems) < 3 {
		t.Fatalf("expected at least 3 problems, got %d: %v", len(ve.Problems), ve.Problems)
	}
}
