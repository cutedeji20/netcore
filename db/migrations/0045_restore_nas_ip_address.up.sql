-- RouterOS sends its local HotSpot address in the NAS-IP-Address RADIUS
-- attribute. radius_source_ip remains the packet-source identity used by the
-- generated FreeRADIUS client declaration; it is not the NAS-IP-Address.

BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM nas AS source_nas
          JOIN nas AS existing_nas
            ON existing_nas.nasname = source_nas.hotspot_address
           AND existing_nas.id <> source_nas.id
         WHERE source_nas.nasname <> source_nas.hotspot_address
    ) THEN
        RAISE EXCEPTION 'cannot restore NAS-IP-Address: hotspot address collides with an existing NAS address';
    END IF;
END;
$$;

UPDATE nas
   SET nasname = hotspot_address
 WHERE nasname <> hotspot_address;

COMMIT;
