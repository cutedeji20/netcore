# RouterOS HotSpot deployment

These are rendered deployment templates, not scripts to import unchanged.
They contain a shared RADIUS secret after rendering and must never be committed.

## Required render values

- `__RADIUS_ADDRESS__` and `__RADIUS_SHARED_SECRET__` — the FreeRADIUS server
  and this router's unique RADIUS secret.
- `__COA_SOURCE_ADDRESS__` — one exact NetCore control-plane source address
  (use a host route such as `10.30.0.10/32`, not a guest or office subnet).
- `__PORTAL_HOST__`, `__PORTAL_ADDRESS__`, and `__PORTAL_ORIGIN__` — the
  walled-garden portal's hostname, resolved address, and HTTPS origin.
- `__HOTSPOT_PROFILE__`, `__HOTSPOT_USER_PROFILE__`, and
  `__HOTSPOT_HTML_DIR__` — existing RouterOS profile names and the full local
  directory that will hold the rendered HotSpot pages.

The provisioning script enables RADIUS accounting and per-session interim
updates, sets the per-profile HTML directory, and accepts Disconnect/CoA only
on UDP 3799. Its router-input firewall rules accept that port only from the
rendered control-plane address, then drop every other source before broader
management-LAN rules can allow it.

## Automatic reconnect for enrolled devices

The rendered profile uses `http-pap,mac` and supplies the deployment-managed
MAC-auth password. A returning device therefore performs a new RADIUS request
without opening the captive portal. NetCore accepts that request only when the
same active device MAC belongs to an active, unexpired, non-exhausted
subscription on the active NAS. Router-only MAC cookies are disabled, so a
suspension, quota exhaustion, device replacement, or expiry is enforced on
the next reconnect.

Phone operating systems must retain a stable MAC for the Data Hub SSID. If a
phone changes its private/randomized MAC, it is intentionally treated as a
different device and directed to the portal.

## Local pages and maintenance

Upload the rendered files as `login.html`, `flogin.html`, and `error.html` to
`__HOTSPOT_HTML_DIR__`. They have no external fonts, images, analytics, or
other dependencies. `flogin.html` is shown after a failed RADIUS login and
`error.html` is shown for a fatal local HotSpot error.

For planned portal work, upload the rendered `maintenance.html.tmpl` as
`login.html` in the same directory. Restore the normal `login.html` after the
work. This gives every new guest a clear, locally served message even when the
external portal is intentionally unavailable. A router cannot reliably detect
that a browser's cross-origin portal navigation failed and then replace that
browser error with a local page, so this operator-controlled switch is
intentional.

## Non-production acceptance checklist

1. Run the assembled FreeRADIUS configuration check in its target image.
2. Import the rendered router configuration on a test NAS and confirm the
   CoA port responds from the approved source and is dropped from another host.
3. Connect a new device: no active plan shows the portal; an active plan
   completes the one-use handoff and receives the expected rate/timeout reply.
4. Verify Start, Interim, Stop, duplicate Interim, and Accounting-On/Off in
   the database and confirm the counter advances only by the final cumulative
   traffic total.
5. Swap in the maintenance page, reconnect a guest, then restore the normal
   page. Confirm existing sessions stay connected throughout the drill.

Do not enable this on a production NAS until the FreeRADIUS durable detail
spool/replay path is configured and these checks pass.

## Optional tethering policy pilot

The tenant tethering setting renders a separate, operator-reviewed RouterOS
artifact. It never imports rules automatically. See
[`tethering-policy.md`](tethering-policy.md) for the strict TTL heuristic,
scoped pilot procedure, protected traffic boundaries, and exact rollback.
