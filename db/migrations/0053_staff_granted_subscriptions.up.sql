BEGIN;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_payment_status_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_payment_status_check
  CHECK (payment_status IN ('UNPAID','PAID','REFUNDED','PARTIAL','GRANTED'));
COMMIT;
