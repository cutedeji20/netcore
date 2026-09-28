package devices

import (
	"errors"
	"testing"
)

func TestDeviceRegistrationNormalizesColonAndRouterOSMACForms(t *testing.T) {
	colon, err := NewRegistration("AA:BB:CC:DD:EE:FF", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	routerOS, err := NewRegistration("aabb.ccdd.eeff", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if colon.NormalizedMAC != "aabbccddeeff" || routerOS.NormalizedMAC != "aabbccddeeff" {
		t.Fatalf("normalized values = %q, %q", colon.NormalizedMAC, routerOS.NormalizedMAC)
	}
}

func TestDeviceRegistrationRejectsUnsafeLabelAndInvalidMAC(t *testing.T) {
	if _, err := NewRegistration("not-a-mac", "Laptop"); err == nil {
		t.Fatal("invalid MAC was accepted")
	}
	if _, err := NewRegistration("AA:BB:CC:DD:EE:FF", string(make([]byte, 121))); err == nil {
		t.Fatal("oversized label was accepted")
	}
}

func TestPOSRegistrationRequiresNamedFactoryDevice(t *testing.T) {
	registered, err := NewPOSRegistration("AA:BB:CC:DD:EE:FF", "Counter 1")
	if err != nil {
		t.Fatal(err)
	}
	if registered.NormalizedMAC != "aabbccddeeff" || registered.Label != "Counter 1" || registered.DeviceType != "POS" {
		t.Fatalf("registration = %+v", registered)
	}
	if _, err := NewPOSRegistration("AA:BB:CC:DD:EE:FF", " "); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("blank POS label error = %v", err)
	}
}
