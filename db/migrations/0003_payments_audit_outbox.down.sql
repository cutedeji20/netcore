BEGIN;
DROP TRIGGER IF EXISTS subscription_events_no_delete ON subscription_events;
DROP TRIGGER IF EXISTS subscription_events_no_update ON subscription_events;
DROP TABLE IF EXISTS audit_logs, ledger_entries, ledger_transactions, ledger_accounts,
  idempotency_keys, outbox_events, webhook_events, vouchers, invoices, payments CASCADE;
DROP FUNCTION IF EXISTS ledger_assert_balanced();
DROP FUNCTION IF EXISTS audit_logs_immutable();
COMMIT;
