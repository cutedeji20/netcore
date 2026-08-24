package main

import (
	"context"
	"testing"

	"github.com/netcore-isp/netcore/internal/config"
	"github.com/netcore-isp/netcore/internal/database"
	"github.com/netcore-isp/netcore/internal/integrations"
)

type workerTestResendResolver struct{}

func (workerTestResendResolver) Resolve(_ context.Context, _ string, provider integrations.Provider) ([]byte, integrations.CredentialMetadata, error) {
	if provider != integrations.ProviderResend {
		return nil, integrations.CredentialMetadata{}, integrations.ErrCredentialInvalid
	}
	return []byte("re_test_secret"), integrations.CredentialMetadata{SenderEmail: "NetCore <access@notify.durabledatahubs.com>"}, nil
}

func TestConfiguredReceiptProcessorUsesTenantResendCredentialResolver(t *testing.T) {
	cfg := &config.Config{
		Payments: config.Payments{WebhookMaxAttempts: 5},
	}
	processor, err := configuredReceiptProcessorWithCredentialResolver(cfg, &database.Pool{}, "tenant-data-hub", workerTestResendResolver{})
	if err != nil || processor == nil {
		t.Fatalf("processor=%v err=%v", processor, err)
	}
}
