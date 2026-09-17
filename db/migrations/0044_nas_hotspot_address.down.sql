BEGIN;

UPDATE nas
   SET nasname = hotspot_address
 WHERE nasname <> hotspot_address;

DROP INDEX IF EXISTS nas_hotspot_address_key;
ALTER TABLE nas DROP COLUMN IF EXISTS hotspot_address;

COMMIT;
