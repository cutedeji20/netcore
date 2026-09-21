BEGIN;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM subscriptions WHERE payment_status = 'GRANTED') THEN
    RAISE EXCEPTION 'cannot roll back while staff-granted subscriptions exist';
  END IF;
END $$;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_payment_status_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_payment_status_check
  CHECK (payment_status IN ('UNPAID','PAID','REFUNDED','PARTIAL'));
COMMIT;
