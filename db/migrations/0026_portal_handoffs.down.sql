BEGIN;

DROP FUNCTION IF EXISTS radius_portal_handoff_consume(text, inet, text);
DROP TABLE IF EXISTS portal_handoffs;

COMMIT;
