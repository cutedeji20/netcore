package main

import (
	"context"
	"testing"

	"github.com/netcore-isp/netcore/internal/config"
	"github.com/netcore-isp/netcore/internal/database"
)

type workerTestSecretResolver struct {
	values map[string]string
	calls  int
}

func (r *workerTestSecretResolver) Resolve(_ context.Context, reference string) (string, error) {
	r.calls++
	return r.values[reference], nil
}

func TestConfiguredReceiptProcessorUsesResendOnlyWhenEnabled(t *testing.T) {
	cfg := &config.Config{
		Secrets: config.Secrets{Backend: "sops"},
		Email: config.Email{
			Provider:        "resend",
			ResendAPIKeyRef: "email.resend.api_key",
			From:            "NetCore <access@notify.durabledatahubs.com>",
		},
		Payments: config.Payments{WebhookMaxAttempts: 5},
	}
	resolver := &workerTestSecretResolver{values: map[string]string{"email.resend.api_key": "re_test_secret"}}
	processor, err := configuredReceiptProcessorWithResolver(context.Background(), cfg, &database.Pool{}, resolver)
	if err != nil || processor == nil || resolver.calls != 1 {
		t.Fatalf("processor=%v err=%v resolver_calls=%d", processor, err, resolver.calls)
	}

	disabled := *cfg
	disabled.Email.Provider = "disabled"
	resolver.calls = 0
	processor, err = configuredReceiptProcessorWithResolver(context.Background(), &disabled, &database.Pool{}, resolver)
	if err != nil || processor != nil || resolver.calls != 0 {
		t.Fatalf("disabled processor=%v err=%v resolver_calls=%d", processor, err, resolver.calls)
	}
}
