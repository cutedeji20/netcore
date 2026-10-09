BEGIN;
DROP INDEX IF EXISTS vouchers_order_position_unique;
ALTER TABLE vouchers
    DROP CONSTRAINT vouchers_order_position_complete,
    DROP CONSTRAINT vouchers_code_envelope_complete,
    DROP COLUMN code_kek_key_id,
    DROP COLUMN code_wrapped_dek,
    DROP COLUMN code_nonce,
    DROP COLUMN code_ciphertext,
    DROP COLUMN order_position,
    DROP COLUMN order_id;
DROP TABLE voucher_batches;
DROP TABLE voucher_orders;
COMMIT;
