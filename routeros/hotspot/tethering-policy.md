# NetCore scoped HotSpot tethering policy

NetCore renders a RouterOS policy from the tenant tethering setting. Rendering
does **not** connect to, import into, or otherwise change a router. An operator
must review the rendered text and apply it deliberately from an out-of-band
management session.

## Inputs and safe defaults

The renderer accepts only:

- `enabled` — defaults to `false` for a new tenant; a disabled policy renders
  no enforcement rule.
- `expected_client_ttl` — defaults to `64` when rendering an unspecified
  policy and must be from `64` to `255`.
- one HotSpot bridge or VLAN interface name supplied by the operator. It must
  be the dedicated guest scope, for example `bridge-guest`; it is never a
  router-wide rule.

The renderer rejects blank, space-containing, or command-like interface names.
Every enforcement rule has an exact `NetCore:tethering:<interface>:...`
comment. Re-importing an enabled script removes only that matching NetCore pair
before adding it again. It places the pair first in their respective forward
chains so an earlier broad accept or FastTrack rule cannot silently bypass the
pilot. The rollback output removes only that same pair.

## What strict mode does

For authenticated forwarded traffic from the supplied HotSpot interface, strict
mode marks packets whose TTL is below the configured direct-client baseline and
drops the marked traffic. A normal directly-connected endpoint normally retains
the baseline TTL; a downstream endpoint forwarded through Android tethering is
normally one hop lower.

The rules intentionally match `chain=forward` and `hotspot=auth`. They do not
target router input, unauthenticated HotSpot traffic, local portal navigation,
DNS before login, payment/walled-garden access, or RouterOS management. Do not
broaden the scope or remove these matchers to troubleshoot an access problem.

TTL is a practical heuristic, not proof of device identity. It can be bypassed
or produce false positives with rooted devices, custom TTL settings, VPNs,
external routers, and unusual network stacks.

## Pilot procedure

1. Complete the existing RADIUS portal acceptance test first. Do not use this
   policy to mask an unresolved login or accounting problem.
2. Pick one isolated guest bridge/VLAN and verify its exact interface name on
   the router. Back up the current router configuration and use Safe Mode from
   an out-of-band management connection.
3. Keep the tenant policy disabled while reviewing the rendered artifact. The
   operator explicitly enables it only for the pilot scope and manually imports
   the reviewed script.
4. Test a direct authenticated phone or laptop: portal handoff, DNS, browsing,
   payment/walled-garden, and logout must continue to work.
5. Test an Android phone sharing that connection with one downstream device:
   the downstream device must not browse while the direct device remains
   connected. Inspect the two NetCore rule counters.
6. If a direct client is blocked, run the rendered rollback exactly as supplied,
   keep Safe Mode available, and investigate counters and TTL observations.
   Never flush firewall rules or disable unrelated HotSpot/RADIUS policies.
7. Expand to more HotSpot scopes only after the full pilot matrix passes.

## Rollback

Use the exact rollback text paired with the rendered artifact. It removes only
the two comments for the selected scope; it does not touch generic firewall,
HotSpot, DNS, payment, RADIUS, or router-management rules.
