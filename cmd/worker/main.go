// Command worker is the NetCore background-process entry point.
//
// The job queue is added with the business workflows in later phases. This
// process is intentionally separate from the HTTP API now, so it has the
// correct lifecycle, dependency ownership, and container entry point before
// those jobs arrive.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/netcore-isp/netcore/internal/cache"
	"github.com/netcore-isp/netcore/internal/config"
	"github.com/netcore-isp/netcore/internal/database"
	"github.com/netcore-isp/netcore/internal/integrations"
	"github.com/netcore-isp/netcore/internal/logger"
	"github.com/netcore-isp/netcore/internal/notify"
	"github.com/netcore-isp/netcore/internal/payments"
)

func main() {
	if err := run(); err != nil {
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

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), cfg.Database.ConnectTimeout)
	defer cancelStartup()
	postgres, err := database.Open(startupCtx, cfg.Database)
	if err != nil {
		return err
	}
	defer postgres.Close()

	redisClient := cache.New(cfg.Redis)
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Warn("redis client close failed", slog.String("error", err.Error()))
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	webhookProcessor, err := configuredWebhookProcessor(startupCtx, cfg, postgres)
	if err != nil {
		return err
	}
	receiptProcessor, err := configuredReceiptProcessor(startupCtx, cfg, postgres)
	if err != nil {
		return err
	}
	if webhookProcessor == nil && receiptProcessor == nil {
		log.Info("worker started", slog.String("queue", "payment queues disabled"))
		<-ctx.Done()
		log.Info("worker shutdown signal received")
		return nil
	}
	log.Info("worker started", slog.Bool("webhooks_enabled", webhookProcessor != nil), slog.Bool("receipts_enabled", receiptProcessor != nil))
	ticker := time.NewTicker(cfg.Payments.WebhookPollInterval)
	defer ticker.Stop()
	for {
		worked := false
		if webhookProcessor != nil {
			webhookWorked, err := webhookProcessor.ProcessOne(ctx)
			if err != nil {
				log.Error("payment webhook processing failed", slog.String("error", err.Error()))
			} else if webhookWorked {
				worked = true
			}
		}
		if receiptProcessor != nil {
			receiptWorked, err := receiptProcessor.ProcessOne(ctx)
			if err != nil {
				log.Error("payment receipt processing failed", slog.String("error", err.Error()))
			} else if receiptWorked {
				worked = true
			}
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			log.Info("worker shutdown signal received")
			return nil
		case <-ticker.C:
		}
	}
}

func configuredWebhookProcessor(ctx context.Context, cfg *config.Config, db *database.Pool) (*payments.WebhookProcessor, error) {
	if cfg == nil || db == nil {
		return nil, fmt.Errorf("payment worker configuration is required")
	}
	tenant, found, err := db.ResolveActiveTenant(ctx, cfg.Portal.TenantSlug)
	if err != nil {
		return nil, fmt.Errorf("resolve webhook portal tenant: %w", err)
	}
	if !found || tenant.ID == "" {
		return nil, fmt.Errorf("webhook portal tenant is not active")
	}
	store, err := integrations.NewPostgresStore(db)
	if err != nil {
		return nil, err
	}
	wrapper, err := configuredIntegrationKeyWrapper(cfg)
	if err != nil {
		return nil, err
	}
	credentials, err := integrations.NewCredentialResolver(store, wrapper)
	if err != nil {
		return nil, err
	}
	gateway, err := payments.NewTenantPaystackGateway(credentials, tenant.ID, nil)
	if err != nil {
		return nil, err
	}
	paymentStore, err := payments.NewPostgresStore(db, true)
	if err != nil {
		return nil, err
	}
	service, err := payments.NewService(paymentStore, gateway)
	if err != nil {
		return nil, err
	}
	return payments.NewWebhookProcessor(paymentStore, service, gateway.Name(), cfg.Payments.WebhookMaxAttempts)
}

func configuredReceiptProcessor(ctx context.Context, cfg *config.Config, db *database.Pool) (*payments.ReceiptProcessor, error) {
	if cfg == nil || db == nil {
		return nil, fmt.Errorf("payment worker configuration is required")
	}
	tenant, found, err := db.ResolveActiveTenant(ctx, cfg.Portal.TenantSlug)
	if err != nil {
		return nil, fmt.Errorf("resolve receipt portal tenant: %w", err)
	}
	if !found || tenant.ID == "" {
		return nil, fmt.Errorf("receipt portal tenant is not active")
	}
	store, err := integrations.NewPostgresStore(db)
	if err != nil {
		return nil, err
	}
	wrapper, err := configuredIntegrationKeyWrapper(cfg)
	if err != nil {
		return nil, err
	}
	credentials, err := integrations.NewCredentialResolver(store, wrapper)
	if err != nil {
		return nil, err
	}
	return configuredReceiptProcessorWithCredentialResolver(cfg, db, tenant.ID, credentials)
}

func configuredReceiptProcessorWithCredentialResolver(cfg *config.Config, db *database.Pool, tenantID string, resolver notify.TenantResendCredentialResolver) (*payments.ReceiptProcessor, error) {
	if cfg == nil || db == nil || tenantID == "" || resolver == nil {
		return nil, fmt.Errorf("payment worker configuration is required")
	}
	notifier, err := notify.NewTenantResendNotifier(resolver, tenantID, nil)
	if err != nil {
		return nil, err
	}
	paymentStore, err := payments.NewPostgresStore(db, true)
	if err != nil {
		return nil, err
	}
	return payments.NewReceiptProcessor(paymentStore, notifier, cfg.Payments.WebhookMaxAttempts)
}
