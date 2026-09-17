-- A HotSpot's local redirect address and its RADIUS packet source are often
-- different when RADIUS travels through WireGuard.  Keep both identities:
-- nasname remains the RADIUS NAS-IP-Address; hotspot_address is the local
-- address embedded in RouterOS's captive-portal redirect.

BEGIN;

ALTER TABLE nas ADD COLUMN hotspot_address inet;

UPDATE nas
   SET hotspot_address = nasname
 WHERE hotspot_address IS NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM nas
         GROUP BY radius_source_ip
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot migrate NAS identities: radius_source_ip must be unique';
    END IF;

    -- Avoid a transient unique-key collision while replacing the historical
    -- local HotSpot address with the RADIUS packet source.
    IF EXISTS (
        SELECT 1
          FROM nas AS source_nas
          JOIN nas AS existing_nas
            ON existing_nas.nasname = source_nas.radius_source_ip
           AND existing_nas.id <> source_nas.id
         WHERE source_nas.nasname <> source_nas.radius_source_ip
    ) THEN
        RAISE EXCEPTION 'cannot migrate NAS identities: radius source collides with an existing NAS address';
    END IF;
END;
$$;

-- Existing deployments historically stored the HotSpot address in nasname.
-- RADIUS authorization/accounting see the packet source instead, so make the
-- source canonical before the next captive-portal handoff.
UPDATE nas
   SET nasname = radius_source_ip
 WHERE nasname <> radius_source_ip;

ALTER TABLE nas ALTER COLUMN hotspot_address SET NOT NULL;
CREATE UNIQUE INDEX nas_hotspot_address_key ON nas (hotspot_address);

COMMIT;
