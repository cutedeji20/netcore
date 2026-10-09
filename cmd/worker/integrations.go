package main

import (
	"errors"

	"github.com/netcore-isp/netcore/internal/config"
	"github.com/netcore-isp/netcore/internal/integrations"
)

func configuredIntegrationKeyWrapper(cfg *config.Config) (integrations.KeyWrapper, error) {
	if cfg == nil {
		return nil, errors.New("integration configuration is required")
	}
	switch cfg.IntegrationCrypto.Backend {
	case "disabled":
		return integrations.NewUnavailableKeyWrapper(), nil
	case "azure-key-vault":
		return integrations.NewManagedIdentityKeyWrapper(cfg.IntegrationCrypto.KEKID)
	default:
		return nil, errors.New("integration crypto backend is invalid")
	}
}

func configuredVoucherKeyWrapper(cfg *config.Config) (integrations.KeyWrapper, error) {
	if cfg == nil {
		return nil, errors.New("voucher configuration is required")
	}
	switch cfg.VoucherCrypto.Backend {
	case "disabled":
		return integrations.NewUnavailableKeyWrapper(), nil
	case "azure-key-vault":
		return integrations.NewManagedIdentityKeyWrapper(cfg.VoucherCrypto.KEKID)
	default:
		return nil, errors.New("voucher crypto backend is invalid")
	}
}
