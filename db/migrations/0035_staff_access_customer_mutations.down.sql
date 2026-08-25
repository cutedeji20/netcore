-- This rollback deliberately preserves roles, people, customer records, and
-- audit history. Encrypted dynamic MFA records cannot be represented by the
-- legacy secret_ref-only schema, so reject rollback until they are migrated.

BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM user_mfa_totp
         WHERE secret_ref IS NULL
           AND (secret_ciphertext IS NOT NULL
                OR secret_nonce IS NOT NULL
                OR wrapped_dek IS NOT NULL
                OR kek_key_id IS NOT NULL)
    ) THEN
        RAISE EXCEPTION 'cannot roll back 0035 while dynamic MFA envelope records exist';
    END IF;
END$$;

DROP POLICY IF EXISTS tenant_isolation ON staff_invitation_mfa;
ALTER TABLE IF EXISTS staff_invitation_mfa DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON staff_invitations;
ALTER TABLE IF EXISTS staff_invitations DISABLE ROW LEVEL SECURITY;
DROP TABLE IF EXISTS staff_invitation_mfa;
DROP TABLE IF EXISTS staff_invitations;

DROP INDEX IF EXISTS customers_tenant_email_key;

ALTER TABLE user_mfa_totp
    DROP CONSTRAINT IF EXISTS user_mfa_totp_secret_representation_coherent,
    DROP COLUMN IF EXISTS secret_ciphertext,
    DROP COLUMN IF EXISTS secret_nonce,
    DROP COLUMN IF EXISTS wrapped_dek,
    DROP COLUMN IF EXISTS kek_key_id,
    ALTER COLUMN secret_ref SET NOT NULL;

COMMIT;
