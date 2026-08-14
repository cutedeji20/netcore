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
	"github.com/netcore-isp/netcore/internal/logger"
	"github.com/netcore-isp/netcore/internal/payments"
	"github.com/netcore-isp/netcore/internal/secrets"
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

	processor, err := configuredWebhookProcessor(startupCtx, cfg, postgres)
	if err != nil {
		return err
	}
	if processor == nil {
		log.Info("worker started", slog.String("queue", "payment webhooks disabled"))
		<-ctx.Done()
		log.Info("worker shutdown signal received")
		return nil
	}
	log.Info("worker started", slog.String("queue", "payment webhooks"))
	ticker := time.NewTicker(cfg.Payments.WebhookPollInterval)
	defer ticker.Stop()
	for {
		worked, err := processor.ProcessOne(ctx)
		if err != nil {
			log.Error("payment webhook processing failed", slog.String("error", err.Error()))
		}
		if worked && err == nil {
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
	if cfg.Payments.Gateway == "disabled" {
		return nil, nil
	}
	if cfg.Payments.Gateway != "paystack" || cfg.Secrets.Backend != "sops" {
		return nil, fmt.Errorf("payment webhook worker has no configured gateway secret store")
	}
	store, err := secrets.NewSOPSFileStore(cfg.Secrets.Ref)
	if err != nil {
		return nil, fmt.Errorf("payment SecretStore: %w", err)
	}
	if _, err := store.Resolve(ctx, cfg.Payments.PaystackSecretRef); err != nil {
		return nil, fmt.Errorf("payment SecretStore reference: %w", err)
	}
	gateway, err := payments.NewPaystackGateway(store, cfg.Payments.PaystackSecretRef, nil)
	if err != nil {
		return nil, err
	}
	paymentStore, err := payments.NewPostgresStore(db)
	if err != nil {
		return nil, err
	}
	service, err := payments.NewService(paymentStore, gateway)
	if err != nil {
		return nil, err
	}
	return payments.NewWebhookProcessor(paymentStore, service, gateway.Name(), cfg.Payments.WebhookMaxAttempts)
}
