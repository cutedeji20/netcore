BEGIN;

UPDATE nas
   SET nasname = hotspot_address
 WHERE nasname <> hotspot_address;

COMMIT;
