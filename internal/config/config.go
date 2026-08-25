// Package config loads and validates runtime configuration.
//
// Spec: BUILD.md §105.
//
// Validation here is not only "is the value present" but "is this combination
// safe for this environment". A misconfigured production start is the cheapest
// incident to prevent and one of the most common; the process refuses to boot
// rather than serve traffic with CORS wide open.
package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Env string

const (
	EnvDevelopment Env = "development"
	EnvStaging     Env = "staging"
	EnvProduction  Env = "production"
)

func (e Env) IsProduction() bool { return e == EnvProduction }

// Config is the fully validated application configuration.
type Config struct {
	Env         Env
	ServiceName string
	HTTPAddr    string

	Database Database
	Redis    Redis
	Security Security
	Auth     Auth
	Quota    Quota
	Limits   Limits
	Secrets  Secrets
	Email    Email
	Portal   Portal
	Payments Payments
	// Staff contains public browser-facing URLs used in staff workflows. It
	// never contains invitation tokens or other secrets.
	Staff Staff
	// IntegrationCrypto contains only the location of the Key Vault KEK used
	// to wrap database-held provider credentials. It never contains a provider
	// credential or an Azure client secret.
	IntegrationCrypto IntegrationCrypto
}

type Database struct {
	DSN              string
	MaxConns         int
	StatementTimeout time.Duration
	ConnectTimeout   time.Duration
}

type Redis struct {
	Addr     string
	Password string
	Timeout  time.Duration
	// RequiredForReadiness is deliberately NOT configurable to true.
	//
	// §48: gating readiness on Redis turns a cache outage into a total
	// outage — every instance drains from the load balancer and §15's
	// per-endpoint degradation policy becomes unreachable. The field exists
	// to document the decision, not to offer it as a choice.
	RequiredForReadiness bool
}

type Security struct {
	AllowedOrigins  []string
	TLSSkipVerify   bool
	SeedDataEnabled bool
	SessionTimeout  time.Duration
	TrustedProxies  []string
}

// Auth contains the current password-hashing and browser-session policy.
// Hash parameters are config, not source code, so they can be raised without
// invalidating existing PHC hashes. Successful login rehashes older hashes.
type Auth struct {
	SessionTTL          time.Duration
	PasswordMemoryKiB   uint32
	PasswordIterations  uint32
	PasswordParallelism uint8
	// RequireMFA prevents a password-only browser session from being created.
	// Production control planes must leave this enabled; development can opt in
	// while an administrator is being bootstrapped.
	RequireMFA bool
}

type Quota struct {
	InterimIntervalBase time.Duration
	InterimIntervalFine time.Duration
	MaxPerSessionBytes  uint64
	MinPerSessionBytes  uint64
	ReaperGrace         time.Duration
}

type Limits struct {
	MaxRequestBytes int64
	MaxWebhookBytes int64
	MaxPageSize     int
	DefaultPageSize int
	HandlerTimeout  time.Duration
	MaxJSONArrayLen int
	ShutdownGrace   time.Duration
}

type Secrets struct {
	// Backend is "sops" (Phase 1, §68.2) or "vault" (after §68.4 fires).
	Backend string
	// Ref is a path into the backend, never a secret value.
	Ref string
}

// Email selects a transactional notifier without placing a provider key in
// process configuration. The logical key reference resolves only from the
// configured deployment secret store.
type Email struct {
	Provider        string
	ResendAPIKeyRef string
	From            string
}

// Portal binds public customer routes to one tenant selected by deployment
// configuration. A request must never provide its own tenant selector.
type Portal struct {
	TenantSlug string
}

// Payments selects a provider without putting a provider credential in
// process configuration. SecretRef is a logical key inside the configured
// SecretStore document; it is never a secret value.
type Payments struct {
	Gateway             string
	PaystackSecretRef   string
	PaystackCallbackURL string
	WebhookPollInterval time.Duration
	WebhookMaxAttempts  int
}

// Staff configures the fixed public entry point for staff invitations.
type Staff struct {
	InviteURL string
}

// IntegrationCrypto controls the envelope-key wrapper for dashboard-managed
// provider credentials. The disabled default permits a staged deployment while
// ensuring no provider can be connected until Key Vault is ready.
type IntegrationCrypto struct {
	Backend string
	KEKID   string
}

// ValidationError aggregates every configuration problem so an operator sees
// all of them at once instead of fixing one per restart.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid configuration:\n  - %s", strings.Join(e.Problems, "\n  - "))
}

// Load reads configuration from the environment and validates it.
// It returns an error rather than calling os.Exit so that tests can exercise
// every validation branch.
func Load(getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	databaseDSN, err := valueFromEnvOrFile(getenv, "NETCORE_DB_DSN")
	if err != nil {
		return nil, err
	}
	redisPassword, err := valueFromEnvOrFile(getenv, "NETCORE_REDIS_PASSWORD")
	if err != nil {
		return nil, err
	}

	c := &Config{
		Env:         Env(strDefault(getenv("NETCORE_ENV"), string(EnvDevelopment))),
		ServiceName: strDefault(getenv("NETCORE_SERVICE_NAME"), "netcore-api"),
		HTTPAddr:    strDefault(getenv("NETCORE_HTTP_ADDR"), ":8080"),

		Database: Database{
			DSN:              databaseDSN,
			MaxConns:         intDefault(getenv("NETCORE_DB_MAX_CONNS"), 25),
			StatementTimeout: durDefault(getenv("NETCORE_DB_STATEMENT_TIMEOUT"), 10*time.Second),
			ConnectTimeout:   durDefault(getenv("NETCORE_DB_CONNECT_TIMEOUT"), 5*time.Second),
		},
		Redis: Redis{
			Addr:                 getenv("NETCORE_REDIS_ADDR"),
			Password:             redisPassword,
			Timeout:              durDefault(getenv("NETCORE_REDIS_TIMEOUT"), 250*time.Millisecond),
			RequiredForReadiness: false, // §48. Not configurable. See the field comment.
		},
		Security: Security{
			AllowedOrigins:  splitList(getenv("NETCORE_ALLOWED_ORIGINS")),
			TLSSkipVerify:   boolDefault(getenv("NETCORE_TLS_SKIP_VERIFY"), false),
			SeedDataEnabled: boolDefault(getenv("NETCORE_SEED_ENABLED"), false),
			SessionTimeout:  durDefault(getenv("NETCORE_SESSION_TIMEOUT"), 4*time.Hour),
			TrustedProxies:  splitList(getenv("NETCORE_TRUSTED_PROXIES")),
		},
		Auth: Auth{
			SessionTTL:          durDefault(getenv("NETCORE_AUTH_SESSION_TTL"), 24*time.Hour),
			PasswordMemoryKiB:   uint32(uintDefault(getenv("NETCORE_AUTH_PASSWORD_MEMORY_KIB"), 64*1024)),
			PasswordIterations:  uint32(uintDefault(getenv("NETCORE_AUTH_PASSWORD_ITERATIONS"), 3)),
			PasswordParallelism: uint8(uintDefault(getenv("NETCORE_AUTH_PASSWORD_PARALLELISM"), 2)),
			RequireMFA:          boolDefault(getenv("NETCORE_AUTH_REQUIRE_MFA"), false),
		},
		Quota: Quota{
			InterimIntervalBase: durDefault(getenv("NETCORE_QUOTA_INTERIM_BASE"), 300*time.Second),
			InterimIntervalFine: durDefault(getenv("NETCORE_QUOTA_INTERIM_FINE"), 60*time.Second),
			MaxPerSessionBytes:  uintDefault(getenv("NETCORE_QUOTA_MAX_SESSION_BYTES"), 2<<30),
			MinPerSessionBytes:  uintDefault(getenv("NETCORE_QUOTA_MIN_SESSION_BYTES"), 32<<20),
			ReaperGrace:         durDefault(getenv("NETCORE_QUOTA_REAPER_GRACE"), 60*time.Second),
		},
		Limits: Limits{
			MaxRequestBytes: int64Default(getenv("NETCORE_MAX_REQUEST_BYTES"), 256*1024),
			MaxWebhookBytes: int64Default(getenv("NETCORE_MAX_WEBHOOK_BYTES"), 64*1024),
			MaxPageSize:     intDefault(getenv("NETCORE_MAX_PAGE_SIZE"), 100),
			DefaultPageSize: intDefault(getenv("NETCORE_DEFAULT_PAGE_SIZE"), 25),
			HandlerTimeout:  durDefault(getenv("NETCORE_HANDLER_TIMEOUT"), 15*time.Second),
			MaxJSONArrayLen: intDefault(getenv("NETCORE_MAX_JSON_ARRAY"), 1000),
			ShutdownGrace:   durDefault(getenv("NETCORE_SHUTDOWN_GRACE"), 20*time.Second),
		},
		Secrets: Secrets{
			Backend: strDefault(getenv("NETCORE_SECRETS_BACKEND"), "sops"),
			Ref:     getenv("NETCORE_SECRETS_REF"),
		},
		Email: Email{
			Provider:        strDefault(getenv("NETCORE_EMAIL_PROVIDER"), "disabled"),
			ResendAPIKeyRef: getenv("NETCORE_RESEND_API_KEY_REF"),
			From:            getenv("NETCORE_EMAIL_FROM"),
		},
		Portal: Portal{
			TenantSlug: getenv("NETCORE_PORTAL_TENANT_SLUG"),
		},
		Payments: Payments{
			Gateway:             strDefault(getenv("NETCORE_PAYMENT_GATEWAY"), "disabled"),
			PaystackSecretRef:   getenv("NETCORE_PAYSTACK_SECRET_REF"),
			PaystackCallbackURL: getenv("NETCORE_PAYSTACK_CALLBACK_URL"),
			WebhookPollInterval: durDefault(getenv("NETCORE_WEBHOOK_POLL_INTERVAL"), time.Second),
			WebhookMaxAttempts:  intDefault(getenv("NETCORE_WEBHOOK_MAX_ATTEMPTS"), 8),
		},
		Staff: Staff{
			InviteURL: strings.TrimSpace(getenv("NETCORE_STAFF_INVITE_URL")),
		},
		IntegrationCrypto: IntegrationCrypto{
			Backend: strDefault(getenv("NETCORE_INTEGRATION_CRYPTO_BACKEND"), "disabled"),
			KEKID:   strings.TrimSpace(getenv("NETCORE_INTEGRATION_KEK_ID")),
		},
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// valueFromEnvOrFile accepts a normal value for local development and a
// newline-terminated mounted secret for production. Supplying both is an
// operator error: silently choosing one could make a rotation appear to work
// while an old credential remains in use.
func valueFromEnvOrFile(getenv func(string) string, key string) (string, error) {
	value := getenv(key)
	path := strings.TrimSpace(getenv(key + "_FILE"))
	if value != "" && path != "" {
		return "", fmt.Errorf("%s and %s_FILE cannot both be set", key, key)
	}
	if path == "" {
		return value, nil
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s_FILE must be an absolute path", key)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", key, err)
	}
	return strings.TrimSpace(string(contents)), nil
}

// Validate enforces both presence and safety. §105.
func (c *Config) Validate() error {
	var p []string

	switch c.Env {
	case EnvDevelopment, EnvStaging, EnvProduction:
	default:
		p = append(p, fmt.Sprintf("NETCORE_ENV %q must be development, staging or production", c.Env))
	}

	if c.Database.DSN == "" {
		p = append(p, "NETCORE_DB_DSN is required")
	}
	if c.Database.MaxConns < 1 {
		p = append(p, "NETCORE_DB_MAX_CONNS must be >= 1")
	}
	if c.Database.StatementTimeout <= 0 {
		p = append(p, "NETCORE_DB_STATEMENT_TIMEOUT must be > 0 (§49: no unbounded queries)")
	}

	// §21A.5: sub-minute interim updates are an accounting flood.
	if c.Quota.InterimIntervalBase < 60*time.Second {
		p = append(p, "NETCORE_QUOTA_INTERIM_BASE must be >= 60s (§21A.5)")
	}
	if c.Quota.InterimIntervalFine < 60*time.Second {
		p = append(p, "NETCORE_QUOTA_INTERIM_FINE must be >= 60s (§21A.5)")
	}
	if c.Quota.InterimIntervalFine > c.Quota.InterimIntervalBase {
		p = append(p, "NETCORE_QUOTA_INTERIM_FINE must not exceed the base interval")
	}
	// v1.2 §21A.3: without a floor the budget asymptotes and the tail of a
	// prepaid bundle can never be consumed.
	if c.Database.ConnectTimeout <= 0 {
		p = append(p, "NETCORE_DB_CONNECT_TIMEOUT must be > 0")
	}
	if c.Redis.Timeout <= 0 {
		p = append(p, "NETCORE_REDIS_TIMEOUT must be > 0")
	}
	if c.Auth.SessionTTL <= 0 {
		p = append(p, "NETCORE_AUTH_SESSION_TTL must be > 0")
	}
	if c.Auth.PasswordMemoryKiB < 8*1024 {
		p = append(p, "NETCORE_AUTH_PASSWORD_MEMORY_KIB must be >= 8192")
	}
	if c.Auth.PasswordIterations < 1 {
		p = append(p, "NETCORE_AUTH_PASSWORD_ITERATIONS must be >= 1")
	}
	if c.Auth.PasswordParallelism < 1 {
		p = append(p, "NETCORE_AUTH_PASSWORD_PARALLELISM must be >= 1")
	}

	if c.Quota.MinPerSessionBytes == 0 {
		p = append(p, "NETCORE_QUOTA_MIN_SESSION_BYTES must be > 0 (§21A.3 asymptote guard)")
	}
	if c.Quota.MaxPerSessionBytes < c.Quota.MinPerSessionBytes {
		p = append(p, "NETCORE_QUOTA_MAX_SESSION_BYTES must be >= the minimum")
	}

	if c.Limits.DefaultPageSize > c.Limits.MaxPageSize {
		p = append(p, "NETCORE_DEFAULT_PAGE_SIZE must not exceed NETCORE_MAX_PAGE_SIZE")
	}
	if c.Limits.MaxPageSize < 1 {
		p = append(p, "NETCORE_MAX_PAGE_SIZE must be >= 1 (§16)")
	}
	if c.Limits.HandlerTimeout <= 0 {
		p = append(p, "NETCORE_HANDLER_TIMEOUT must be > 0 (§49)")
	}
	if c.Limits.MaxWebhookBytes < 1024 || c.Limits.MaxWebhookBytes > 64*1024 || c.Limits.MaxWebhookBytes > c.Limits.MaxRequestBytes {
		p = append(p, "NETCORE_MAX_WEBHOOK_BYTES must be between 1024 and 65536 and not exceed NETCORE_MAX_REQUEST_BYTES")
	}
	if c.Payments.WebhookPollInterval < 250*time.Millisecond || c.Payments.WebhookPollInterval > time.Minute {
		p = append(p, "NETCORE_WEBHOOK_POLL_INTERVAL must be between 250ms and 1m")
	}
	if c.Payments.WebhookMaxAttempts < 1 || c.Payments.WebhookMaxAttempts > 20 {
		p = append(p, "NETCORE_WEBHOOK_MAX_ATTEMPTS must be between 1 and 20")
	}
	switch c.Payments.Gateway {
	case "disabled":
	case "paystack":
		if !validSecretReference(c.Payments.PaystackSecretRef) {
			p = append(p, "NETCORE_PAYSTACK_SECRET_REF must be a logical secret reference when NETCORE_PAYMENT_GATEWAY=paystack")
		}
		if !validHTTPSURL(c.Payments.PaystackCallbackURL) {
			p = append(p, "NETCORE_PAYSTACK_CALLBACK_URL must be a valid HTTPS URL when NETCORE_PAYMENT_GATEWAY=paystack")
		} else if !callbackOriginAllowed(c.Payments.PaystackCallbackURL, c.Security.AllowedOrigins) {
			p = append(p, "NETCORE_PAYSTACK_CALLBACK_URL must use an origin listed in NETCORE_ALLOWED_ORIGINS")
		}
		if c.Secrets.Ref == "" {
			p = append(p, "NETCORE_SECRETS_REF is required when NETCORE_PAYMENT_GATEWAY=paystack")
		}
	default:
		p = append(p, fmt.Sprintf("NETCORE_PAYMENT_GATEWAY %q must be disabled or paystack", c.Payments.Gateway))
	}
	switch c.Email.Provider {
	case "disabled":
	case "resend":
		if !validSecretReference(c.Email.ResendAPIKeyRef) {
			p = append(p, "NETCORE_RESEND_API_KEY_REF must be a logical secret reference when NETCORE_EMAIL_PROVIDER=resend")
		}
		if _, err := mail.ParseAddress(strings.TrimSpace(c.Email.From)); err != nil {
			p = append(p, "NETCORE_EMAIL_FROM must be a valid sender when NETCORE_EMAIL_PROVIDER=resend")
		}
		if c.Secrets.Ref == "" {
			p = append(p, "NETCORE_SECRETS_REF is required when NETCORE_EMAIL_PROVIDER=resend")
		}
		if !validPortalTenantSlug(c.Portal.TenantSlug) {
			p = append(p, "NETCORE_PORTAL_TENANT_SLUG must be a lowercase tenant slug when NETCORE_EMAIL_PROVIDER=resend")
		}
	default:
		p = append(p, fmt.Sprintf("NETCORE_EMAIL_PROVIDER %q must be disabled or resend", c.Email.Provider))
	}

	switch c.IntegrationCrypto.Backend {
	case "disabled":
		if c.IntegrationCrypto.KEKID != "" {
			p = append(p, "NETCORE_INTEGRATION_KEK_ID must be empty when NETCORE_INTEGRATION_CRYPTO_BACKEND=disabled")
		}
	case "azure-key-vault":
		if !validAzureKeyVaultKeyID(c.IntegrationCrypto.KEKID) {
			p = append(p, "NETCORE_INTEGRATION_KEK_ID must be a versioned Azure Key Vault key URL when NETCORE_INTEGRATION_CRYPTO_BACKEND=azure-key-vault")
		}
	default:
		p = append(p, fmt.Sprintf("NETCORE_INTEGRATION_CRYPTO_BACKEND %q must be disabled or azure-key-vault", c.IntegrationCrypto.Backend))
	}
	if c.Staff.InviteURL != "" && !validStaffInviteURL(c.Staff.InviteURL, c.Security.AllowedOrigins) {
		p = append(p, "NETCORE_STAFF_INVITE_URL must be a fragment-free HTTPS URL without a query whose origin is listed in NETCORE_ALLOWED_ORIGINS")
	}
	if c.Env.IsProduction() && c.IntegrationCrypto.Backend == "azure-key-vault" && c.Staff.InviteURL == "" {
		p = append(p, "NETCORE_STAFF_INVITE_URL is required in production when NETCORE_INTEGRATION_CRYPTO_BACKEND=azure-key-vault")
	}
	if c.Portal.TenantSlug != "" && !validPortalTenantSlug(c.Portal.TenantSlug) {
		p = append(p, "NETCORE_PORTAL_TENANT_SLUG must be a lowercase tenant slug when configured")
	}

	// ------------------------------------------------------------------
	// Production safety invariants. These are the reason this function
	// exists — presence checks alone would let all of these through.
	// ------------------------------------------------------------------
	if c.Env.IsProduction() {
		if c.Email.Provider != "disabled" {
			p = append(p, "NETCORE_EMAIL_PROVIDER must be disabled in production; use a dashboard-managed encrypted integration")
		}
		if c.Payments.Gateway != "disabled" {
			p = append(p, "NETCORE_PAYMENT_GATEWAY must be disabled in production; use a dashboard-managed encrypted integration")
		}
		if !c.Auth.RequireMFA {
			p = append(p, "NETCORE_AUTH_REQUIRE_MFA must be true in production (privileged operator access)")
		}
		if c.Security.TLSSkipVerify {
			p = append(p, "NETCORE_TLS_SKIP_VERIFY must be false in production (§1.18)")
		}
		if c.Security.SeedDataEnabled {
			p = append(p, "NETCORE_SEED_ENABLED must be false in production (§104)")
		}
		if len(c.Security.AllowedOrigins) == 0 {
			p = append(p, "NETCORE_ALLOWED_ORIGINS is required in production (§57: default deny)")
		}
		for _, o := range c.Security.AllowedOrigins {
			if o == "*" {
				p = append(p, "NETCORE_ALLOWED_ORIGINS must not contain '*' in production (§1.19, §57)")
			}
			if strings.HasPrefix(o, "http://") {
				p = append(p, fmt.Sprintf("origin %q must use https in production", o))
			}
		}
		if len(c.Security.TrustedProxies) == 0 {
			p = append(p, "NETCORE_TRUSTED_PROXIES is required in production (§45: proxy source validation)")
		}
		for _, proxy := range c.Security.TrustedProxies {
			if !validTrustedProxy(proxy) {
				p = append(p, fmt.Sprintf("NETCORE_TRUSTED_PROXIES contains invalid proxy %q", proxy))
			}
		}
		if c.Redis.Addr == "" {
			p = append(p, "NETCORE_REDIS_ADDR is required in production (§15 rate limiting)")
		}
		if strings.TrimSpace(c.Redis.Password) == "" {
			p = append(p, "NETCORE_REDIS_PASSWORD or NETCORE_REDIS_PASSWORD_FILE is required in production (§15 rate limiting)")
		}
		if c.Secrets.Ref == "" {
			p = append(p, "NETCORE_SECRETS_REF is required in production (§68)")
		}
		if strings.Contains(c.Database.DSN, "sslmode=disable") {
			p = append(p, "database DSN must not disable TLS in production (§1.18)")
		}
		// §82.3: Session-Timeout IS the outage exposure window. An 8-hour
		// value means 8 hours of potentially unbilled access during a total
		// control-plane outage. Bound it so nobody sets it to 72h casually.
		if c.Security.SessionTimeout > 24*time.Hour {
			p = append(p, "NETCORE_SESSION_TIMEOUT must be <= 24h (§82.3: this is your outage exposure window)")
		}
	}

	if c.Secrets.Backend != "sops" && c.Secrets.Backend != "vault" {
		p = append(p, fmt.Sprintf("NETCORE_SECRETS_BACKEND %q must be sops or vault (§68)", c.Secrets.Backend))
	}

	if len(p) > 0 {
		return &ValidationError{Problems: p}
	}
	return nil
}

func validTrustedProxy(value string) bool {
	value = strings.TrimSpace(value)
	if address, err := netip.ParseAddr(value); err == nil {
		return !address.IsUnspecified()
	}
	prefix, err := netip.ParsePrefix(value)
	return err == nil && !prefix.Addr().IsUnspecified()
}

func validHTTPSURL(value string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func validAzureKeyVaultKeyID(value string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if !strings.HasSuffix(host, ".vault.azure.net") || strings.TrimSuffix(host, ".vault.azure.net") == "" {
		return false
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	return len(parts) == 3 && parts[0] == "keys" && parts[1] != "" && parts[2] != ""
}

func callbackOriginAllowed(value string, allowedOrigins []string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	origin := parsed.Scheme + "://" + parsed.Host
	for _, allowed := range allowedOrigins {
		if allowed == origin {
			return true
		}
	}
	return false
}

func validStaffInviteURL(value string, allowedOrigins []string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	origin := parsed.Scheme + "://" + parsed.Host
	for _, allowed := range allowedOrigins {
		if allowed == origin {
			return true
		}
	}
	return false
}

// ErrMissing is returned by MustLoad's caller path when configuration cannot
// be satisfied. Exposed so cmd/ can distinguish config failure from others.
var ErrMissing = errors.New("configuration invalid")

func strDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return strings.TrimSpace(v)
}

func intDefault(v string, d int) int {
	if v == "" {
		return d
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return d
	}
	return n
}

func int64Default(v string, d int64) int64 {
	if v == "" {
		return d
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return d
	}
	return n
}

func uintDefault(v string, d uint64) uint64 {
	if v == "" {
		return d
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return d
	}
	return n
}

func boolDefault(v string, d bool) bool {
	if v == "" {
		return d
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return d
	}
	return b
}

func durDefault(v string, d time.Duration) time.Duration {
	if v == "" {
		return d
	}
	x, err := time.ParseDuration(v)
	if err != nil {
		return d
	}
	return x
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func validSecretReference(value string) bool {
	if len(value) < 3 || len(value) > 160 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validPortalTenantSlug(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}
