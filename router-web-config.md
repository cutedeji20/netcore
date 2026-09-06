# NetCore router ↔ web-app configuration

This is the operational guide for connecting a **MikroTik RouterOS v7** HotSpot
to the NetCore web application. It complements [router_setup.md](router_setup.md):
that document is the broader staged RouterOS deployment guide; this one explains
the exact relationship between the customer web portal, RouterOS, FreeRADIUS,
and the NetCore database.

Use a dedicated test SSID, VLAN, or test router first. Do not connect a live
customer router until every staging gate in this document has passed.

> **Current delivery boundary**
>
> Authorised staff can add a router/NAS record from **Network & AAA**, then
> download a one-time setup package after password and MFA confirmation. The
> dashboard stores only an encrypted RADIUS-secret envelope; it never displays
> or retains the secret. It does **not** edit the live FreeRADIUS
> `clients.conf`, connect to RouterOS, or start the FreeRADIUS profile. Apply
> the downloaded package over the private management path, complete the test,
> then record the result before activating AAA. Do not bypass this workflow by
> editing production tables or putting secrets in `.env`, Git, screenshots, or
> chat.

## 1. What is being connected

```text
                         Public HTTPS (443)
Guest device  ───────>  hotspot.durabledatahubs.com
     │                         │
     │ Wi-Fi / guest VLAN      ├── Caddy ──> NetCore UI + API
     ▼                         │                 │
MikroTik HotSpot               │                 └── PostgreSQL / Redis
     │                          │
     │ private VPN only         │ creates one-use portal handoff
     ▼                          │
FreeRADIUS writer ─────────────┘
     │
     ├── durable accounting spool
     └── FreeRADIUS replay ──> PostgreSQL policy/accounting functions
```

There are two separate connections. They must not be confused.

| Connection | Purpose | Network rule |
|---|---|---|
| Guest browser → NetCore portal | Account sign-in, plan selection, payment, and the one-use Wi-Fi handoff | Public HTTPS only, through `https://hotspot.durabledatahubs.com` |
| RouterOS → FreeRADIUS | Access approval and Start/Interim/Stop accounting | UDP 1812/1813 only over a private VPN or private management path |
| NetCore control plane → RouterOS | Future Disconnect/CoA capability | UDP 3799 only from one approved private control-plane address; do not expose it publicly |

Only Caddy exposes public TCP ports 80 and 443. PostgreSQL, Redis, the API,
and FreeRADIUS management interfaces are not public services. Never expose
RADIUS UDP ports to the public Azure IP address.

## 2. The trust boundary, in plain language

The router does not trust the browser to approve Internet access, and the web
app never receives the router's RADIUS secret.

1. RouterOS catches an unauthenticated device on the guest network.
2. Its **local** `login.html` page gathers RouterOS values: the device MAC,
   the NAS/HotSpot address, and the local RouterOS login URL.
3. That page sends the browser only to the approved NetCore portal hostname.
4. The customer signs in (or completes the supported purchase flow) on the
   web app. NetCore checks that the customer has an active subscription.
5. NetCore returns a very short-lived, single-use handoff in a RouterOS login
   URL. The token is bound to that MAC address and NAS.
6. RouterOS forwards the handoff to FreeRADIUS using `http-pap`.
7. FreeRADIUS asks one narrow PostgreSQL function to consume the handoff and
   return the authorised rate, timeout, quota, and filter settings.
8. RouterOS grants or rejects the session. It sends accounting records through
   the durable FreeRADIUS spool.

The handoff is not a customer password. It cannot be replayed on another
device or another NAS, and it is not a reason to weaken the dashboard or
portal session cookie settings.

## 3. Before beginning: owner, scope, and rollback

Assign these roles before changing a router:

| Role | Responsibility |
|---|---|
| NetCore administrator | Adds the router/NAS, obtains the one-time setup package with password + MFA, and records the private acceptance test. |
| Infrastructure operator | Creates the private path and manually applies the downloaded `clients.conf`/RouterOS material. |
| Router operator | Makes the on-site RouterOS changes from an out-of-band management connection and keeps a rollback path. |
| Test operator | Connects a test device and records only redacted acceptance evidence. |

Before any import, make both backups from a local console, serial connection,
WinBox MAC access, or another management path that is not inside the guest
VLAN:

```routeros
/export file=pre-netcore-hotspot
/system backup save name=pre-netcore-hotspot
```

Download both files to protected storage. A HotSpot, firewall, or VLAN mistake
can lock an operator out of a remotely managed router.

## 4. Secure values worksheet

Record these values in the approved secret/network record, not this repository
and not the customer portal. Give the staging router and the production router
different values.

| Value | Meaning | Example form, not a value to copy |
|---|---|---|
| Portal origin | The one public portal/API origin available before guest login | `https://hotspot.durabledatahubs.com` |
| Portal host | The matching hostname for the HotSpot walled garden | `hotspot.durabledatahubs.com` |
| Portal address | Current reserved public IPv4 for the portal host, used only by the quota-only rule | `X.X.X.X` |
| Guest bridge/VLAN | Dedicated client-facing HotSpot network | `bridge-guest` or `vlan-120` |
| HotSpot server address | The address shown by RouterOS as `$(server-address)` | `10.x.x.1` |
| Router RADIUS packet source | Source address from which the router actually reaches FreeRADIUS over the VPN | private tunnel address |
| FreeRADIUS address | Private VPN/control-plane address of the FreeRADIUS host | private tunnel address |
| NAS address | The RouterOS NAS-IP/HotSpot address which the portal and database will bind to | an IPv4 address, never a hostname |
| RADIUS secret | A random, high-entropy secret unique to this one router | secret-manager value only |
| CoA source | One NetCore private control-plane `/32` | `10.x.x.x/32` |
| HotSpot profile | Existing RouterOS HotSpot profile name | `hsprof-guest` |
| HotSpot user profile | Existing RouterOS HotSpot user profile name | `hsprof1` |
| Local HTML directory | RouterOS directory that holds the rendered local HotSpot pages | `hotspot/netcore` |

### Important: the two router addresses

The **RADIUS packet source** authorises the router as a FreeRADIUS `client`.
The **NAS address** binds the portal handoff and later accounting records to the
tenant's active NAS record. They can differ when the router uses a private
tunnel but its HotSpot lives on a guest subnet.

Do not guess that these must be the same. During staging, capture one
Access-Request and verify all three values together:

- the packet source seen by FreeRADIUS;
- `NAS-IP-Address` in the request; and
- the `$(server-address)` sent by the local HotSpot page.

The NAS record must use the address that NetCore validates as `NAS-IP-Address`.
The generated `freeradius/clients.conf` entry must authorise the actual packet
source. If these do not match the planned values, stop and correct the RouterOS
routing/source-address design before any customer test.

## 5. Prepare the NetCore web application

Complete this on the NetCore dashboard and production VM before touching the
router.

### 5.1 Confirm the web deployment is healthy

On the Azure VM:

```bash
cd /srv/netcore/src
docker compose -f deployments/production/compose.yaml ps
curl -fsS --max-time 15 -o /dev/null -w '%{http_code}\n' \
  https://hotspot.durabledatahubs.com/portal.html
```

The portal request must return `200`. PostgreSQL, Redis, API, worker, UI, and
Caddy must be healthy or running. Do not continue if the app is showing a
locked dashboard, if API health is failing, or if the portal host is not
available over HTTPS.

### 5.2 Prepare customer-facing information

From the authenticated dashboard:

1. Confirm the workspace name, timezone, currency, and portal domain are
   correct.
2. Create or review the plan that will be used for staging. It needs a clear
   duration, speed, device/session limits, and quota policy.
3. Confirm the plan is published. Retiring a plan only prevents new sales; it
   does not intentionally cut off an existing entitled subscriber.
4. Confirm the test customer's email and password flow works. Resend is needed
   for customer verification, recovery, and receipts; its sender domain should
   already be verified.
5. If testing an actual web purchase, configure Paystack only through
   **Settings → Integrations** and use Paystack test credentials first. Never
   add payment keys to `.env` or a router.

An account alone is not Wi-Fi entitlement. The handoff needs an **active
subscription**. Router/NAS onboarding is available from Network & AAA, but
general subscription assignment still requires the supported purchase or an
approved audited process. If Paystack is not configured, limit the router
exercise to connectivity and negative RADIUS tests until an active test
subscription can be created through an approved process. Do not insert
subscriptions manually in the production database.

### 5.3 Set the portal's local deployment data

The customer portal must know its tenant from trusted deployment configuration,
not from a URL parameter sent by a guest device. Confirm the deployed
production configuration has the correct portal domain and tenant slug. The
portal must remain same-origin with its API so the secure portal session cookie
works without loosening `HttpOnly`, `Secure`, or `SameSite` protections.

## 6. Prepare the private RADIUS path

### 6.1 Establish the network path first

Use either an existing site-to-site VPN or a dedicated RouterOS v7 WireGuard
tunnel. The intended route is:

```text
router private/VPN source → Azure private/VPN address → FreeRADIUS
```

It is **not**:

```text
public internet → Azure public IP → UDP 1812/1813/3799
```

The private-path design must:

- route only the required control-plane addresses through the tunnel;
- prevent guest clients from reaching router management services;
- limit UDP 1812 and 1813 at the Azure network security group and host firewall
  to the declared router RADIUS packet source; and
- keep UDP 3799 closed unless and until a specific NetCore CoA source is
  actively used.

Do not open a broad office subnet, `0.0.0.0/0`, or an Azure public Internet rule
as a shortcut.

### 6.2 Register and render the NAS configuration

Use Network & AAA and the protected infrastructure procedure together:

1. Select **Add router**. Enter only private management, NAS, and RADIUS
   packet-source addresses from the worksheet. The NAS begins disabled.
2. Select **Configure AAA**, complete password + MFA confirmation, then choose
   **Download setup**. This rotates the per-router secret and downloads the
   only plaintext copy.
3. Apply the FreeRADIUS client block and RouterOS command in that package. The
   RouterOS command pins `src-address` to the registered packet source; do not
   replace it with a public address.
4. The application does not write the runtime client file. Put the downloaded
   FreeRADIUS client block through the controlled rendering procedure below.
5. Place the rendered secret-bearing file at:

   ```text
   /srv/netcore/runtime/radius/clients.conf
   ```

6. Set it to UID/GID `101:101` and mode `0400`. Do not commit it.

The RADIUS database password is a separate mounted runtime secret:
`/srv/netcore/runtime/radius/db_password`. It has the same value as the
PostgreSQL Radius password but is deliberately a separate file with different
ownership. Do not combine or loosen these files.

### 6.3 Validate FreeRADIUS before it receives router traffic

The RADIUS profile is intentionally opt-in. First confirm the spool is on a
dedicated, monitored filesystem and has enough capacity for its configured
maximum plus free-space reserve. Then validate the exact production image:

```bash
cd /srv/netcore/src
docker compose -f deployments/production/compose.yaml --profile radius build radius radius-replay
docker compose -f deployments/production/compose.yaml --profile radius run --rm -e NETCORE_RADIUS_VALIDATE=1 radius
docker compose -f deployments/production/compose.yaml --profile radius run --rm -e NETCORE_RADIUS_VALIDATE=1 radius-replay
```

Only after each validation succeeds, start the staged RADIUS pair:

```bash
docker compose -f deployments/production/compose.yaml --profile radius up -d radius radius-replay
```

Confirm `radius`, `radius-replay`, and `radius-spool-init` are healthy or have
completed as expected. The writer must persist accounting to the durable spool
before acknowledging it; the replay service later applies records to
PostgreSQL. Do not clear a non-empty spool to hide an error.

## 7. Prepare RouterOS safely

### 7.1 Isolate the guest network

Create or select a dedicated guest bridge/VLAN and SSID. Never attach HotSpot
to the WAN, an interface carrying router management traffic, or a bridge that
also exposes staff systems.

On the intended staging router, inspect the existing layout before changing it:

```routeros
/interface bridge print detail
/interface vlan print detail
/ip address print detail
/ip pool print detail
/ip dhcp-server print detail
/ip hotspot print detail
```

Guest devices should receive an address and be able to resolve the portal host,
but they must not reach the router management LAN. Configure the guest bridge,
address pool, DHCP server, NAT/uplink policy, and access point SSID according
to the site's network design. Those interface names and subnets are
site-specific, so do not paste a generic bridge/VLAN creation script into a
production router.

### 7.2 Create the staging HotSpot

Run the RouterOS wizard **only** on the isolated guest bridge/VLAN:

```routeros
/ip hotspot setup
```

Record the created server, HotSpot profile, user profile, and HotSpot address:

```routeros
/ip hotspot print detail
/ip hotspot profile print detail
/ip hotspot user profile print detail
```

The wizard may create a temporary local HotSpot user. Remove or disable it
before promotion. Local HotSpot users can be checked before RADIUS and can
become an unintentional access bypass.

### 7.3 RB5009: replace the default MikroTik sign-in page

The screenshot showing the MikroTik username/password page means the RB5009
HotSpot is already intercepting unauthenticated guest traffic. Keep that
working HotSpot; replace only the HTML it serves. The NetCore local page then
opens the NetCore portal, where the customer signs in and pays. It does **not**
store a customer password or payment key on the router.

Do this first on a test SSID/VLAN. This first phase proves that the default
MikroTik page has been replaced. It does not yet authorise customers until the
private RADIUS steps in sections 6, 8, and 9 are complete.

1. On the RB5009, use WinBox through the management LAN, MAC connection, or
   another out-of-band path. Do not manage the router from the guest Wi-Fi.
2. Capture the current HotSpot server and profile names:

   ```routeros
   /ip hotspot print detail
   /ip hotspot profile print detail
   /ip hotspot user profile print detail
   /file print detail
   ```

   Note the HotSpot server's `profile`, its `html-directory` or
   `html-directory-override`, and the address shown as the HotSpot server
   address. Do not change the existing `dns-name` just to replace the page.
3. In **WinBox → Files**, create a separate local directory for the new pages,
   for example `hotspot/netcore`. Keeping the MikroTik default `hotspot`
   directory unchanged makes the first rollback simple.
4. Make a rendered copy of these repository templates on the controlled
   workstation:

   | Template | RB5009 file |
   |---|---|
   | `routeros/hotspot/login.html.tmpl` | `login.html` |
   | `routeros/hotspot/flogin.html.tmpl` | `flogin.html` |
   | `routeros/hotspot/error.html.tmpl` | `error.html` |

   In the `login.html` copy, replace the one literal
   `__PORTAL_ORIGIN__` with:

   ```text
   https://hotspot.durabledatahubs.com
   ```

   Do not edit the RouterOS expressions such as `$(mac)` or
   `$(server-address)`. They are filled in locally by the RB5009 when a guest
   connects. Do not add analytics, external fonts, images, scripts, or a
   tenant parameter to this page.
5. Upload those three rendered files to the new `hotspot/netcore` directory in
   **WinBox → Files**. Do not delete the original MikroTik files.
6. Point only the identified HotSpot profile at the new local directory. Use
   the real profile and directory names from step 2:

   ```routeros
   /ip hotspot profile set [find where name="<HOTSPOT_PROFILE>"] \
       html-directory-override="<HOTSPOT_HTML_DIRECTORY>"
   ```

   For example, `<HOTSPOT_HTML_DIRECTORY>` may be `hotspot/netcore`. Do not
   run this against every HotSpot profile on a multi-site router.
7. Allow only the NetCore portal host before authentication. The command below
   removes a previous NetCore rule by its comment, then creates exactly one
   replacement; it does not open general Internet access:

   ```routeros
   /ip hotspot walled-garden remove [find where comment="netcore-portal"]
   /ip hotspot walled-garden add action=allow \
       dst-host=hotspot.durabledatahubs.com comment="netcore-portal"
   ```

8. From a new test device on the guest SSID, forget/rejoin the Wi-Fi and open
   an ordinary HTTP address if the phone does not show its captive-portal
   assistant automatically. The default MikroTik page should now be replaced
   by the NetCore portal at `https://hotspot.durabledatahubs.com/portal.html`.
9. If the portal fails to load, immediately restore the previous local HTML
   directory on that HotSpot profile. Check the guest DNS, the exact
   walled-garden hostname, and that the page contains the production HTTPS
   origin—not a test value or unresolved placeholder.

At this point the visual replacement is complete. The portal may correctly
tell a customer that no active plan is available; that is safe. Do not switch
off the HotSpot, create a permissive local user, or allow the whole Internet to
make the page appear to work.

### 7.4 Make payment a real access gate

Replacing `login.html` alone cannot charge a customer or grant access. The
access rule becomes real only when all of the following are true:

1. **Paystack is ready.** In the NetCore administrator dashboard, open
   **Settings → Integrations → Paystack**, connect with test credentials first,
   and complete its current-password/MFA confirmation. The encrypted provider
   record stays in the database; neither the RB5009 nor a `.env` file receives
   the Paystack secret.
2. **Paystack can notify NetCore.** In Paystack, set the webhook URL to:

   ```text
   https://hotspot.durabledatahubs.com/webhooks/paystack
   ```

   Confirm signed `charge.success` notifications are accepted. The customer
   return page is `https://hotspot.durabledatahubs.com/portal.html`; returning
   to that page is not proof of payment. NetCore performs server-to-server
   verification before activating a subscription.
3. **The plan is published and the customer has an active subscription.** A
   successful payment is what creates the entitlement; an account or a visible
   plan is not enough.
4. **FreeRADIUS is running behind the private path.** The RB5009 must be a
   registered NAS with its own rendered client entry and secret. Its RADIUS
   Access-Request then receives an Access-Accept only for the short-lived
   portal handoff of an active subscription.

Once those four conditions are met, the flow is: guest joins Wi-Fi → local
RB5009 page opens NetCore portal → customer signs in or pays → NetCore verifies
the payment and activates the subscription → portal returns a one-use handoff
to the RB5009 → RADIUS allows the Internet with the plan limits.

If Paystack is not configured yet, keep the RB5009 on the staging SSID and
complete only the visual-portal and rejection tests. That is preferable to
allowing unpaid users online.

## 8. Render the NetCore RouterOS assets

The repository templates are deliberately not importable as-is. Rendering must
occur on a controlled workstation or deployment process because the resulting
RouterOS `.rsc` file contains a RADIUS secret.

| Source template | Rendered result | Where it goes |
|---|---|---|
| `routeros/hotspot/provision-hotspot.rsc.tmpl` | one secret-bearing RouterOS import file | upload temporarily, import, then delete |
| `routeros/hotspot/login.html.tmpl` | `login.html` | RouterOS local HotSpot HTML directory |
| `routeros/hotspot/flogin.html.tmpl` | `flogin.html` | same directory |
| `routeros/hotspot/error.html.tmpl` | `error.html` | same directory |
| `routeros/hotspot/maintenance.html.tmpl` | temporary maintenance `login.html` | same directory, only during planned portal work |

Render every one of these values in the provisioning script:

```text
__RADIUS_ADDRESS__
__RADIUS_SHARED_SECRET__
__COA_SOURCE_ADDRESS__
__PORTAL_HOST__
__PORTAL_ADDRESS__
__HOTSPOT_PROFILE__
__HOTSPOT_USER_PROFILE__
__HOTSPOT_HTML_DIR__
```

Render `__PORTAL_ORIGIN__` in the HTML templates as:

```text
https://hotspot.durabledatahubs.com
```

Before upload, check the rendered files for any remaining `__PLACEHOLDER__`
text. Review the output without copying secrets into a ticket or terminal
history. Keep the rendered `.rsc` encrypted until import and delete temporary
copies according to the secret-handling procedure.

### What the script deliberately configures

The rendered `provision-hotspot.rsc` script:

- uses `http-pap,mac-cookie`, not `http-chap`; the handoff design requires
  `http-pap`;
- enables RADIUS and RADIUS accounting;
- accepts RADIUS-selected interim accounting intervals;
- limits the local MAC cookie to one minute; session and quota rules still win;
- configures the unique RADIUS server/secret;
- allows the portal hostname, and no broad Internet destination, through the
  unauthenticated walled garden;
- creates the quota-exhausted portal-only firewall chain; and
- limits RouterOS incoming CoA/Disconnect on UDP 3799 to one rendered private
  control-plane source and drops every other source.

Do not replace the walled-garden entry with `*`, a public resolver range,
social-media domains, payment-provider domains, or a whole subnet.

## 9. Import and verify on the staging router

1. Upload the rendered `.rsc`, `login.html`, `flogin.html`, and `error.html`
   files through WinBox **Files** or a secure management transfer. Place the
   HTML files in the local HTML directory from the worksheet.
2. Enter RouterOS Safe Mode from the out-of-band management session.
3. Import the one-time RouterOS script:

   ```routeros
   /import file-name=<rendered-netcore-hotspot-file>.rsc
   ```

4. Verify the actual values; do not assume a successful import is correct:

   ```routeros
   /ip hotspot profile print detail
   /ip hotspot user profile print detail
   /radius print detail
   /radius incoming print detail
   /ip hotspot walled-garden print detail where comment="netcore-portal"
   /ip firewall filter print detail where comment~"netcore-coa|netcore-quota"
   ```

5. Confirm exactly one `netcore-portal` walled-garden rule exists. Repeated
   imports require a review of the resulting RouterOS rules.
6. Verify the private route to the declared RADIUS address without exposing a
   service publicly. Confirm the router sends packets through the intended
   tunnel and that the Azure firewall/NSG sees only the declared private source.
7. Delete the imported secret-bearing `.rsc` from RouterOS **Files** after
   verification. RouterOS retains the configured RADIUS secret; the import file
   must not remain downloadable.
8. Leave Safe Mode only when the management connection and guest VLAN still
   work as expected.

## 10. What happens from router to web app, then back again

This is the complete customer journey to use when testing.

| Step | Router / guest browser action | NetCore action | Expected result |
|---|---|---|---|
| 1 | A phone joins the guest SSID and receives a guest IP | None yet | It cannot reach the general Internet. |
| 2 | RouterOS HotSpot serves its local `login.html` | None yet | The local page derives MAC, NAS address, and RouterOS login URL. |
| 3 | Browser navigates to the one configured portal origin | Caddy serves portal/UI/API over HTTPS | `portal.html` loads even before Internet access. |
| 4 | Customer signs in, verifies email, recovers account, or starts a supported purchase | Portal uses the trusted tenant configuration and secure same-origin cookie | The browser never chooses a tenant or arbitrary return URL. |
| 5 | Customer has an active subscription | `POST /api/v1/portal/handoff` validates the local RouterOS context | The API creates a short-lived, MAC- and NAS-bound one-use handoff. |
| 6 | Browser follows the returned local RouterOS login URL | None | The URL contains the one permitted short-lived token-in-URL exception and should not be logged. |
| 7 | RouterOS sends Access-Request to FreeRADIUS | FreeRADIUS atomically consumes the handoff and returns rate/limit attributes | An active customer gets Access-Accept; wrong NAS/MAC, expired, or replayed handoff gets Access-Reject. |
| 8 | RouterOS opens the allowed session | FreeRADIUS receives accounting Start/Interim/Stop | Usage/session evidence is written through durable spool and replay. |

The local `login.html` template never sends a tenant slug from the browser. It
constructs the portal request from RouterOS variables and redirects only to the
rendered NetCore origin. The portal validates the local RouterOS login URL host
against the supplied NAS address to prevent use as an arbitrary redirector.

## 11. Staging acceptance checklist

Record the result, time, router identity, software release, and operator. Do
not record RADIUS secrets, handoff tokens, customer passwords, or raw RouterOS
form bodies.

### Basic web/portal checks

- [ ] A new guest device receives a guest IP and sees the NetCore portal.
- [ ] `https://hotspot.durabledatahubs.com/portal.html` loads before login.
- [ ] Other websites remain blocked before login.
- [ ] A customer can register/sign in and receive expected Resend messages if
      that email path is part of the test.
- [ ] The customer account alone does not obtain Internet access without an
      active subscription.

### Authorisation and accounting checks

- [ ] An active test subscription produces the expected speed, session timeout,
      quota, and device/session behaviour.
- [ ] Expired, replayed, wrong-MAC, and wrong-NAS handoffs are rejected.
- [ ] RADIUS Start, Interim, Stop, duplicate Interim, Accounting-On, and
      Accounting-Off appear correctly; an identical retransmit does not
      double-count usage.
- [ ] The router receives RADIUS accounting responses only over the private
      path.
- [ ] A quota-exhausted test leaves only the portal renewal route reachable;
      it does not try to intercept HTTPS.
- [ ] A one-minute MAC cookie does not outlive RADIUS session or quota limits.

### Durability and security checks

- [ ] While replay-to-PostgreSQL is temporarily unavailable, the spool grows
      and the router still receives an accounting acknowledgement after the
      durable writer records it.
- [ ] Once replay recovers, the spool drains without duplicates or lost usage.
- [ ] The writer stops rather than acknowledging packets if the configured
      spool capacity guard is reached.
- [ ] UDP 1812/1813 are unreachable from the public Internet.
- [ ] UDP 3799 is dropped from every source except the approved private
      control-plane address. If CoA is not in use yet, keep the host-side path
      closed as well.
- [ ] Replacing `login.html` with the local maintenance page affects new
      sign-ins only; existing connected customers continue as designed.

Do not call a successful portal redirect a launch approval. It proves only the
browser portion of the path, not RADIUS authorisation or durable accounting.

## 12. Production promotion

Promote only after the staging checklist is fully green and the infrastructure
operator approves the exact router values.

1. Create new production-only NAS and secret values. Never reuse staging
   secrets.
2. Render a production-only FreeRADIUS client entry and RouterOS import file.
3. Start with a small customer-facing guest VLAN/SSID and a scheduled window.
4. Keep one operator on the router and one operator monitoring API,
   FreeRADIUS writer/replay, spool capacity, and the database.
5. Verify one controlled customer journey, disconnect/expiry, and complete
   accounting cycle before expanding to all clients.
6. Rotate the router RADIUS secret immediately if an import file could have
   been exposed, then re-render `clients.conf` and the RouterOS configuration.

## 13. Rollback and maintenance

### Rollback

If any portal, authorisation, accounting, firewall, or management-access check
fails:

1. Stop admitting new guests to the affected test SSID/VLAN, or restore the
   protected pre-change RouterOS backup.
2. For a planned portal outage only, upload the rendered local maintenance page
   as `login.html`. Do not leave guests facing an unexplained browser error.
3. Do not delete NAS records, accounting spool files, customer data, or
   secret-manager entries during investigation.
4. Preserve redacted evidence and record the affected router/time/impact.
5. Rotate the per-router RADIUS secret when its confidentiality is in doubt.

### Planned portal maintenance

RouterOS cannot reliably detect a browser's failed external portal navigation
and replace it with a local page. For planned work, deliberately upload the
rendered `maintenance.html` template as the local `login.html`; restore the
normal local `login.html` after the work. Existing sessions are not changed by
that page replacement.

## 14. Troubleshooting without weakening security

| Symptom | Safe first checks | Never do this |
|---|---|---|
| Guest cannot see the portal | Confirm correct guest bridge/VLAN, local HTML directory, DNS, and the single `netcore-portal` walled-garden entry. | Add a wildcard walled-garden rule or give the guest unrestricted Internet. |
| Portal loads but customer cannot get online | Confirm active subscription, correct NAS address, private route, FreeRADIUS profile health, and handoff rejection evidence. | Paste a RADIUS secret into browser/devtools/logs or accept any local HotSpot user. |
| RADIUS authentication does not reach FreeRADIUS | Verify private route, packet source, `clients.conf` source IP, Azure NSG/host firewall, and router secret identity. | Open UDP 1812/1813 to the Internet or use one secret for all routers. |
| Portal handoff rejects unexpectedly | Compare `$(server-address)`, RADIUS `NAS-IP-Address`, and the NAS record using a controlled staging trace. | Change a production NAS address blindly or bypass the database function. |
| Sessions or usage are missing | Check UDP 1813, writer/replay health, spool free space/age, Start/Interim/Stop records, and database connectivity. | Clear or rewrite the accounting spool. |
| CoA is received from an unapproved host | Treat it as a security incident: disable the listener/path, inspect RouterOS firewall order and VPN routes, then rotate the RADIUS secret. | Broaden the source rule to “fix” it. |

## 15. Dashboard boundary and future work

Network & AAA now provides audited router/NAS onboarding and a one-time setup
download. It does not make a live network test by itself:

- an operator must apply the package over the private management path;
- FreeRADIUS `clients.conf` remains a protected runtime file, updated through
  the reviewed infrastructure process;
- activation requires fresh MFA and an administrator attestation that the
  private Access-Request and Start/Interim/Stop accounting test succeeded;
- a queued, audited Disconnect/CoA action remains separate work.

## References

- [RouterOS HotSpot template guide](routeros/hotspot/README.md)
- [FreeRADIUS deployment guide](freeradius/README.md)
- [Portal integration contract](docs/portal-integration.md)
- [Production RADIUS spool/replay gates](deployments/production/README.md)
- [Existing staged RouterOS guide](router_setup.md)
- [MikroTik HotSpot documentation](https://help.mikrotik.com/docs/spaces/ROS/pages/56459266/HotSpot%2B-%2BCaptive%2Bportal)
- [MikroTik RADIUS documentation](https://help.mikrotik.com/docs/spaces/ROS/pages/328097/RADIUS)
