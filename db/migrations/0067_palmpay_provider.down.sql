BEGIN;

DROP INDEX IF EXISTS integration_providers_one_payment_default_idx;
ALTER TABLE integration_providers DROP COLUMN IF EXISTS is_payment_default;

DELETE FROM integration_providers WHERE provider = 'palmpay';

ALTER TABLE integration_providers
  DROP CONSTRAINT integration_providers_provider_check,
  ADD CONSTRAINT integration_providers_provider_check
    CHECK (provider IN ('resend', 'paystack', 'squad'));

ALTER TABLE integration_providers
  DROP CONSTRAINT integration_providers_provider_metadata_coherent,
  ADD CONSTRAINT integration_providers_provider_metadata_coherent CHECK (
    (provider = 'resend' AND sender_email IS NOT NULL AND paystack_mode IS NULL)
    OR
    (provider IN ('paystack', 'squad') AND sender_email IS NULL AND paystack_mode IS NOT NULL)
  );

COMMIT;
