BEGIN;

ALTER TABLE integration_providers
  DROP CONSTRAINT integration_providers_provider_check,
  ADD CONSTRAINT integration_providers_provider_check
    CHECK (provider IN ('resend', 'paystack', 'squad', 'palmpay'));

ALTER TABLE integration_providers
  DROP CONSTRAINT integration_providers_provider_metadata_coherent,
  ADD CONSTRAINT integration_providers_provider_metadata_coherent CHECK (
    (provider = 'resend' AND sender_email IS NOT NULL AND paystack_mode IS NULL)
    OR
    (provider IN ('paystack', 'squad', 'palmpay') AND sender_email IS NULL AND paystack_mode IS NOT NULL)
  );

ALTER TABLE integration_providers
  ADD COLUMN is_payment_default boolean NOT NULL DEFAULT false;

CREATE UNIQUE INDEX integration_providers_one_payment_default_idx
  ON integration_providers (tenant_id)
  WHERE is_payment_default;

COMMIT;
