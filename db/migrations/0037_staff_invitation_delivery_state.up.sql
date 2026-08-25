-- An invitation credential is never redeemable until delivery is durably
-- recorded. DELIVERY_PENDING is deliberately excluded from public lookup.
BEGIN;

ALTER TABLE staff_invitations DROP CONSTRAINT staff_invitations_status_check;
ALTER TABLE staff_invitations ADD CONSTRAINT staff_invitations_status_check
    CHECK (status IN ('DELIVERY_PENDING', 'PENDING', 'REDEEMED', 'REVOKED'));

ALTER TABLE staff_invitations DROP CONSTRAINT staff_invitations_redemption_coherent;
ALTER TABLE staff_invitations ADD CONSTRAINT staff_invitations_redemption_coherent CHECK (
    (status = 'REDEEMED' AND redeemed_at IS NOT NULL)
    OR (status IN ('DELIVERY_PENDING', 'PENDING', 'REVOKED') AND redeemed_at IS NULL)
);

DROP INDEX staff_invitations_one_pending_email_idx;
CREATE UNIQUE INDEX staff_invitations_one_live_email_idx
    ON staff_invitations (tenant_id, email)
    WHERE status IN ('DELIVERY_PENDING', 'PENDING');

COMMIT;
