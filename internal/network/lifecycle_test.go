package network

import "testing"

func TestRouterCreateInputNormalizesPrivateAddresses(t *testing.T) {
	input := RouterCreateInput{
		Name:           "  RB5009-LK-01  ",
		ManagementIP:   "172.16.0.4",
		NASIPAddress:   "172.16.0.4",
		RadiusSourceIP: "172.16.1.9",
	}
	if err := input.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate: %v", err)
	}
	if input.Name != "RB5009-LK-01" || input.ManagementIP != "172.16.0.4" || input.NASIPAddress != "172.16.0.4" || input.RadiusSourceIP != "172.16.1.9" {
		t.Fatalf("input was not normalized: %+v", input)
	}
}

func TestRouterCreateInputRejectsPublicOrAmbiguousAddresses(t *testing.T) {
	valid := RouterCreateInput{Name: "RB5009-LK-01", ManagementIP: "172.16.0.4", NASIPAddress: "172.16.0.4", RadiusSourceIP: "172.16.1.9"}
	for _, change := range []struct {
		name  string
		apply func(*RouterCreateInput)
	}{
		{name: "public management", apply: func(v *RouterCreateInput) { v.ManagementIP = "40.123.254.20" }},
		{name: "public nas", apply: func(v *RouterCreateInput) { v.NASIPAddress = "8.8.8.8" }},
		{name: "public source", apply: func(v *RouterCreateInput) { v.RadiusSourceIP = "1.1.1.1" }},
		{name: "blank name", apply: func(v *RouterCreateInput) { v.Name = " " }},
	} {
		t.Run(change.name, func(t *testing.T) {
			input := valid
			change.apply(&input)
			if err := input.NormalizeAndValidate(); err == nil {
				t.Fatal("invalid router input was accepted")
			}
		})
	}
}

func TestRadiusCredentialRecordRequiresCompleteEnvelope(t *testing.T) {
	record := RadiusCredentialRecord{
		TenantID: "11111111-1111-4111-8111-111111111111",
		NASID:    "22222222-2222-4222-8222-222222222222",
		Version:  1,
		Envelope: RadiusCredentialEnvelope{Ciphertext: []byte("ciphertext"), Nonce: make([]byte, 12), WrappedDEK: []byte("wrapped"), KEKKeyID: "https://vault.example/keys/netcore-provider-kek/version"},
	}
	if !record.Valid() {
		t.Fatal("complete credential record was rejected")
	}
	record.Envelope.KEKKeyID = ""
	if record.Valid() {
		t.Fatal("incomplete credential record was accepted")
	}
}
