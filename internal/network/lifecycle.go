package network

import (
	"net/netip"
	"strings"
)

const maxRouterNameLength = 120

var privateRouterPrefixes = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fd00::/8"),
}

// RouterCreateInput is non-secret metadata for a provisioning router and its
// disabled NAS. NASIPAddress and RadiusSourceIP remain distinct for VPN/NAT.
type RouterCreateInput struct {
	Name           string
	SiteID         string
	ManagementIP   string
	NASIPAddress   string
	RadiusSourceIP string
}

// NormalizeAndValidate accepts only address ranges permitted by routers.
func (in *RouterCreateInput) NormalizeAndValidate() error {
	if in == nil {
		return ErrInvalidRouterInput
	}
	in.Name = strings.TrimSpace(in.Name)
	in.SiteID = strings.TrimSpace(in.SiteID)
	in.ManagementIP = strings.TrimSpace(in.ManagementIP)
	in.NASIPAddress = strings.TrimSpace(in.NASIPAddress)
	in.RadiusSourceIP = strings.TrimSpace(in.RadiusSourceIP)
	if in.Name == "" || len(in.Name) > maxRouterNameLength || !privateRouterAddress(in.ManagementIP) || !privateRouterAddress(in.NASIPAddress) || !privateRouterAddress(in.RadiusSourceIP) {
		return ErrInvalidRouterInput
	}
	if in.SiteID != "" && !validNetworkID(in.SiteID) {
		return ErrInvalidRouterInput
	}
	return nil
}

func privateRouterAddress(raw string) bool {
	address, err := netip.ParseAddr(raw)
	if err != nil {
		return false
	}
	for _, prefix := range privateRouterPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// RadiusCredentialRecord is encrypted material persisted for one NAS.
type RadiusCredentialRecord struct {
	TenantID string
	NASID    string
	Version  int64
	Envelope RadiusCredentialEnvelope
}

func (record RadiusCredentialRecord) Valid() bool {
	return validNetworkID(record.TenantID) && validNetworkID(record.NASID) && record.Version > 0 && len(record.Envelope.Ciphertext) > 0 && len(record.Envelope.Nonce) == 12 && len(record.Envelope.WrappedDEK) > 0 && strings.TrimSpace(record.Envelope.KEKKeyID) != ""
}
