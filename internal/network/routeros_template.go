package network

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrInvalidTetheringScope = errors.New("network: invalid HotSpot bridge or interface scope")
	ErrInvalidTetheringTTL   = errors.New("network: invalid expected client TTL")
)

// RouterOSTetheringTemplate is operator-reviewed RouterOS source. Rendering it
// never connects to or changes a router.
type RouterOSTetheringTemplate struct {
	Apply    string
	Rollback string
}

var routerOSInterfaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// RenderRouterOSTetheringTemplate produces a scoped, idempotent policy. The
// supplied interface must be the dedicated HotSpot bridge/VLAN; global scopes
// and free-form RouterOS expressions are intentionally unsupported.
func RenderRouterOSTetheringTemplate(policy TetheringPolicy, hotspotInterface string) (RouterOSTetheringTemplate, error) {
	hotspotInterface = strings.TrimSpace(hotspotInterface)
	if !routerOSInterfaceName.MatchString(hotspotInterface) {
		return RouterOSTetheringTemplate{}, ErrInvalidTetheringScope
	}

	ttl := policy.ExpectedClientTTL
	if ttl == 0 {
		ttl = 64
	}
	if ttl < 64 || ttl > 255 {
		return RouterOSTetheringTemplate{}, ErrInvalidTetheringTTL
	}

	markComment := fmt.Sprintf("NetCore:tethering:%s:mark", hotspotInterface)
	dropComment := fmt.Sprintf("NetCore:tethering:%s:drop", hotspotInterface)
	rollback := fmt.Sprintf(`/ip firewall mangle remove [find where comment="%s"]
/ip firewall filter remove [find where comment="%s"]
`, markComment, dropComment)

	if !policy.Enabled {
		return RouterOSTetheringTemplate{
			Apply: fmt.Sprintf(`# NetCore tethering policy is disabled by policy.
# No enforcement rules are added. This is a rendered operator artifact only.
# HotSpot scope: %s; expected direct-client TTL baseline: %d.
`, hotspotInterface, ttl),
			Rollback: rollback,
		}, nil
	}

	return RouterOSTetheringTemplate{
		Apply: fmt.Sprintf(`# NetCore RouterOS HotSpot tethering policy (operator-applied; not automatic).
# Scope is limited to this dedicated HotSpot bridge/VLAN: %s.
# Only authenticated forward traffic with a TTL below %d is dropped. Router input,
# unauthenticated portal/DNS/walled-garden traffic, and HotSpot login traffic are excluded.
# Re-importing is idempotent: it first removes only this exact NetCore rule pair.
%s/ip firewall mangle add chain=forward in-interface=%s hotspot=auth ttl=less-than:%d action=mark-packet new-packet-mark=netcore-tethered-%s passthrough=yes comment="%s" place-before=0
/ip firewall filter add chain=forward in-interface=%s hotspot=auth packet-mark=netcore-tethered-%s action=drop comment="%s" place-before=0
`, hotspotInterface, ttl, rollback, hotspotInterface, ttl, hotspotInterface, markComment, hotspotInterface, hotspotInterface, dropComment),
		Rollback: rollback,
	}, nil
}
