// Command api is the NetCore ISP Platform HTTP API.
//
// Spec: BUILD.md §112 (Phase 1 foundation).
//
// Runtime scope: configuration, datastore wiring, logging, health, security
// middleware, and graceful shutdown.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/automations"
	"github.com/netcore-isp/netcore/internal/billing"
	"github.com/netcore-isp/netcore/internal/cache"
	"github.com/netcore-isp/netcore/internal/config"
	"github.com/netcore-isp/netcore/internal/customers"
	"github.com/netcore-isp/netcore/internal/database"
	"github.com/netcore-isp/netcore/internal/health"
	"github.com/netcore-isp/netcore/internal/integrations"
	"github.com/netcore-isp/netcore/internal/logger"
	"github.com/netcore-isp/netcore/internal/network"
	"github.com/netcore-isp/netcore/internal/notify"
	"github.com/netcore-isp/netcore/internal/payments"
	"github.com/netcore-isp/netcore/internal/plans"
	"github.com/netcore-isp/netcore/internal/portal"
	"github.com/netcore-isp/netcore/internal/secrets"
	"github.com/netcore-isp/netcore/internal/security"
	"github.com/netcore-isp/netcore/internal/sessions"
	"github.com/netcore-isp/netcore/internal/subscriptions"
	"github.com/netcore-isp/netcore/internal/team"
	"github.com/netcore-isp/netcore/internal/vouchers"
	"github.com/netcore-isp/netcore/internal/workspace"
	"github.com/netcore-isp/netcore/pkg/crypto/argon2id"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		// Configuration failures must be loud and specific. §105: fail
		// immediately rather than serve traffic with unsafe settings.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	log := logger.New(os.Stdout, logger.Options{
		Level:       slog.LevelInfo,
		ServiceName: cfg.ServiceName,
		Env:         string(cfg.Env),
	})
	slog.SetDefault(log)

	log.Info("starting",
		slog.String("addr", cfg.HTTPAddr),
		slog.String("env", string(cfg.Env)),
		slog.String("secrets_backend", cfg.Secrets.Backend),
	)

	// §48: PostgreSQL is the only readiness-critical dependency. Redis is
	// reported but never gates rotation — see internal/health for why.
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), cfg.Database.ConnectTimeout)
	defer cancelStartup()

	postgres, err := database.Open(startupCtx, cfg.Database)
	if err != nil {
		return err
	}
	defer postgres.Close()

	// Constructing a Redis client never dials it. A Redis outage must not stop
	// an otherwise healthy API instance from entering rotation.
	redisClient := cache.New(cfg.Redis)
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Warn("redis client close failed", slog.String("error", err.Error()))
		}
	}()

	passwordHasher, err := argon2id.New(argon2id.Params{
		Memory:      cfg.Auth.PasswordMemoryKiB,
		Iterations:  cfg.Auth.PasswordIterations,
		Parallelism: cfg.Auth.PasswordParallelism,
		SaltLength:  16,
		KeyLength:   32,
	})
	if err != nil {
		return fmt.Errorf("password hashing configuration: %w", err)
	}
	authStore, err := auth.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	// The same Key Vault KEK protects every database-held envelope, including
	// dynamic MFA. A disabled wrapper fails dynamic enrollment closed while the
	// legacy secret-reference verification path stays available.
	keyWrapper, err := configuredIntegrationKeyWrapper(cfg)
	if err != nil {
		return err
	}
	authService, err := auth.NewService(authStore, passwordHasher, cfg.Auth.SessionTTL)
	if err != nil {
		return err
	}
	if cfg.Auth.RequireMFA {
		secretStore, err := secrets.NewSOPSFileStore(cfg.Secrets.Ref)
		if err != nil {
			return fmt.Errorf("MFA SecretStore: %w", err)
		}
		mfaService, err := auth.NewMFAService(authStore, secretStore, keyWrapper)
		if err != nil {
			return err
		}
		if err := authService.RequireMFA(mfaService); err != nil {
			return err
		}
	}
	authHTTP, err := auth.NewHTTP(authService, redisClient, cfg.Env != config.EnvDevelopment, cfg.Security.AllowedOrigins, cfg.Security.TrustedProxies)
	if err != nil {
		return err
	}
	integrationStore, err := integrations.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	integrationCredentialResolver, err := integrations.NewCredentialResolver(integrationStore, keyWrapper)
	if err != nil {
		return err
	}
	integrationValidator, err := integrations.NewHTTPProviderValidator(nil)
	if err != nil {
		return err
	}
	integrationService, err := integrations.NewService(integrationStore, keyWrapper, authService, integrationValidator)
	if err != nil {
		return err
	}
	integrationHTTP, err := integrations.NewHTTP(integrationService)
	if err != nil {
		return err
	}
	accountHTTP, err := configuredAccountHTTP(startupCtx, cfg, authStore, authService, passwordHasher, redisClient, integrationCredentialResolver)
	if err != nil {
		return err
	}
	customerStore, err := customers.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	customerHTTP, err := customers.NewHTTP(customerStore, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	subscriptionStore, err := subscriptions.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	subscriptionHTTP, err := subscriptions.NewHTTP(subscriptionStore, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	planStore, err := plans.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	planHTTP, err := plans.NewHTTP(planStore, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	sessionStore, err := sessions.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	sessionHTTP, err := sessions.NewHTTP(sessionStore, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	billingStore, err := billing.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	billingHTTP, err := billing.NewHTTP(billingStore, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	networkStore, err := network.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	networkHTTP, err := network.NewHTTP(networkStore, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	voucherStore, err := vouchers.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	voucherHTTP, err := vouchers.NewHTTP(voucherStore, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	teamStore, err := team.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	teamHTTP, err := team.NewHTTP(teamStore, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	teamInvitationSender, err := notify.NewTenantInvitationSender(integrationCredentialResolver, nil)
	if err != nil {
		return err
	}
	teamService, err := team.NewService(teamStore, keyWrapper, authService, teamInvitationSender, passwordHasher, cfg.Staff.InviteURL)
	if err != nil {
		return err
	}
	if err := teamHTTP.ConfigureInvitations(teamService, redisClient); err != nil {
		return err
	}
	activityStore, err := security.NewActivityPostgresStore(postgres)
	if err != nil {
		return err
	}
	activityHTTP, err := security.NewActivityHTTP(activityStore, func(ctx context.Context) (string, bool) {
		principal, ok := auth.PrincipalFromContext(ctx)
		return principal.TenantID, ok
	}, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	automationStore, err := automations.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	automationHTTP, err := automations.NewHTTP(automationStore, cfg.Limits.DefaultPageSize, cfg.Limits.MaxPageSize)
	if err != nil {
		return err
	}
	workspaceStore, err := workspace.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	workspaceHTTP, err := workspace.NewHTTP(workspaceStore)
	if err != nil {
		return err
	}
	portalStore, err := portal.NewPostgresStore(postgres)
	if err != nil {
		return err
	}
	portalService, err := portal.NewService(portalStore)
	if err != nil {
		return err
	}
	portalHTTP, err := portal.NewHTTP(portalService, redisClient, cfg.Security.AllowedOrigins)
	if err != nil {
		return err
	}
	portalAccountService, err := portal.NewAccountService(portalStore)
	if err != nil {
		return err
	}
	portalAccountHTTP, err := portal.NewAccountHTTP(portalAccountService)
	if err != nil {
		return err
	}
	var catalogueHTTP *portal.CatalogueHTTP
	if cfg.Portal.TenantSlug != "" {
		catalogueService, err := portal.NewCatalogueService(portalStore, cfg.Portal.TenantSlug)
		if err != nil {
			return err
		}
		catalogueHTTP, err = portal.NewCatalogueHTTP(catalogueService)
		if err != nil {
			return err
		}
	}
	paymentStore, err := payments.NewPostgresStore(postgres, true)
	if err != nil {
		return err
	}
	paymentGateway, webhookGateway, err := configuredPaymentGateway(startupCtx, cfg, authStore, integrationCredentialResolver)
	if err != nil {
		return err
	}
	paymentService, err := payments.NewService(paymentStore, paymentGateway, cfg.Payments.PaystackCallbackURL)
	if err != nil {
		return err
	}
	paymentHTTP, err := payments.NewHTTP(paymentService, cfg.Security.AllowedOrigins)
	if err != nil {
		return err
	}
	paymentReadinessHTTP, err := payments.NewReadinessHTTP(paymentGateway, cfg.Payments.PaystackCallbackURL)
	if err != nil {
		return err
	}
	var paymentWebhookHTTP *payments.WebhookHTTP
	if webhookGateway != nil {
		paymentWebhookHTTP, err = payments.NewWebhookHTTP(webhookGateway, paymentStore, cfg.Limits.MaxWebhookBytes)
		if err != nil {
			return err
		}
	}

	h := health.New(cfg.ServiceName, 2*time.Second,
		health.CheckerFunc{
			NameVal:     "postgres",
			CriticalVal: true,
			Fn:          postgres.Ping,
		},
		health.CheckerFunc{
			NameVal:     "redis",
			CriticalVal: false,
			Fn:          redisClient.Ping,
		},
	)

	mux := http.NewServeMux()
	h.Routes(mux)
	authHTTP.Routes(mux)
	if accountHTTP != nil {
		accountHTTP.Routes(mux)
	}
	if err := customerHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := subscriptionHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := planHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := sessionHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := billingHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := networkHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := voucherHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := teamHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := activityHTTP.Routes(mux, authHTTP.RequireAuth, auth.RequirePermission); err != nil {
		return err
	}
	if err := automationHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := workspaceHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := integrationHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := portalHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := portalAccountHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if catalogueHTTP != nil {
		catalogueHTTP.Routes(mux)
	}
	if err := paymentHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if err := paymentReadinessHTTP.Routes(mux, authHTTP); err != nil {
		return err
	}
	if paymentWebhookHTTP != nil {
		if err := paymentWebhookHTTP.Routes(mux); err != nil {
			return err
		}
	}

	handler := security.Chain(mux,
		security.Standard(
			cfg.Security.AllowedOrigins,
			cfg.Limits.MaxRequestBytes,
			cfg.Limits.HandlerTimeout,
		)...,
	)

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: handler,
		// §49: no unbounded network operations, inbound or outbound.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received", slog.Duration("grace", cfg.Limits.ShutdownGrace))
	}

	// Drain in-flight requests before exiting so a deploy does not sever a
	// customer's payment callback mid-flight.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Limits.ShutdownGrace)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	log.Info("stopped cleanly")
	return nil
}

// configuredAccountHTTP binds public customer verification and recovery e-mail
// to the dashboard-managed Resend credential of the configured portal tenant.
// The public routes remain available when no provider is connected, but every
// attempted delivery fails closed without exposing the configuration state.
type accountRuntimeStore interface {
	auth.LoginLimiter
	auth.OTPChallengeStore
}

func configuredAccountHTTP(ctx context.Context, cfg *config.Config, store auth.AccountStore, loginService *auth.Service, hasher argon2id.Hasher, runtime accountRuntimeStore, credentials *integrations.CredentialResolver) (*auth.AccountHTTP, error) {
	if cfg == nil || store == nil || credentials == nil {
		return nil, errors.New("account e-mail configuration is required")
	}
	tenantID, found, err := store.ResolveTenant(ctx, cfg.Portal.TenantSlug)
	if err != nil {
		return nil, fmt.Errorf("resolve portal tenant: %w", err)
	}
	if !found || tenantID == "" {
		return nil, errors.New("portal tenant is not active")
	}
	notifier, err := notify.NewTenantResendNotifier(credentials, tenantID, nil)
	if err != nil {
		return nil, err
	}
	otpService, err := auth.NewOTPService(runtime, notifier)
	if err != nil {
		return nil, err
	}
	accountService, err := auth.NewAccountService(store, hasher, otpService)
	if err != nil {
		return nil, err
	}
	return auth.NewAccountHTTP(accountService, loginService, runtime, cfg.Portal.TenantSlug, cfg.Env != config.EnvDevelopment, cfg.Security.AllowedOrigins, cfg.Security.TrustedProxies)
}

// configuredPaymentGateway binds checkout and webhook verification to the
// active Paystack credential configured for the public portal tenant. It does
// not resolve or retain any payment key at process startup.
func configuredPaymentGateway(ctx context.Context, cfg *config.Config, store auth.AccountStore, credentials *integrations.CredentialResolver) (payments.Gateway, payments.WebhookGateway, error) {
	if cfg == nil || store == nil || credentials == nil {
		return nil, nil, errors.New("payment configuration is required")
	}
	tenantID, found, err := store.ResolveTenant(ctx, cfg.Portal.TenantSlug)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve payment portal tenant: %w", err)
	}
	if !found || tenantID == "" {
		return nil, nil, errors.New("payment portal tenant is not active")
	}
	gateway, err := payments.NewTenantPaystackGateway(credentials, tenantID, nil)
	if err != nil {
		return nil, nil, err
	}
	return gateway, gateway, nil
}

// healthcheck is the Docker health-check entry point. It probes liveness over
// loopback so it verifies the serving process rather than merely checking
// whether a binary can be executed.
func healthcheck() error {
	addr := os.Getenv("NETCORE_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	url, err := healthURL(addr)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("liveness endpoint returned %s", resp.Status)
	}
	return nil
}

func healthURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid NETCORE_HTTP_ADDR %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/health/live", nil
}
