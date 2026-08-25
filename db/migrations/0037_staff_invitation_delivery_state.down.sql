BEGIN;
UPDATE staff_invitations SET status = 'REVOKED' WHERE status = 'DELIVERY_PENDING';
DROP INDEX staff_invitations_one_live_email_idx;
CREATE UNIQUE INDEX staff_invitations_one_pending_email_idx ON staff_invitations (tenant_id, email) WHERE status = 'PENDING';
ALTER TABLE staff_invitations DROP CONSTRAINT staff_invitations_redemption_coherent;
ALTER TABLE staff_invitations ADD CONSTRAINT staff_invitations_redemption_coherent CHECK (
    (status = 'REDEEMED' AND redeemed_at IS NOT NULL)
    OR (status IN ('PENDING', 'REVOKED') AND redeemed_at IS NULL)
);
ALTER TABLE staff_invitations DROP CONSTRAINT staff_invitations_status_check;
ALTER TABLE staff_invitations ADD CONSTRAINT staff_invitations_status_check CHECK (status IN ('PENDING', 'REDEEMED', 'REVOKED'));
COMMIT;
