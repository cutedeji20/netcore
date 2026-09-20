package network

import (
	"strings"
	"testing"
)

func TestRenderRouterOSTetheringTemplateDefaultsDisabled(t *testing.T) {
	template, err := RenderRouterOSTetheringTemplate(TetheringPolicy{}, "bridge-guest")
	if err != nil {
		t.Fatalf("RenderRouterOSTetheringTemplate() error = %v", err)
	}
	if !strings.Contains(template.Apply, "disabled by policy") {
		t.Fatalf("default template must be disabled: %q", template.Apply)
	}
	if strings.Contains(template.Apply, " action=mark-packet") || strings.Contains(template.Apply, " action=drop") {
		t.Fatalf("default template must not add enforcement rules: %q", template.Apply)
	}
}

func TestRenderRouterOSTetheringTemplateValidatesBaselineAndScope(t *testing.T) {
	for _, input := range []struct {
		policy TetheringPolicy
		scope  string
	}{
		{TetheringPolicy{Enabled: true, ExpectedClientTTL: 63}, "bridge-guest"},
		{TetheringPolicy{Enabled: true, ExpectedClientTTL: 256}, "bridge-guest"},
		{TetheringPolicy{Enabled: true, ExpectedClientTTL: 64}, "bridge guest; /system reboot"},
		{TetheringPolicy{Enabled: true, ExpectedClientTTL: 64}, ""},
	} {
		if _, err := RenderRouterOSTetheringTemplate(input.policy, input.scope); err == nil {
			t.Fatalf("RenderRouterOSTetheringTemplate(%+v, %q) unexpectedly succeeded", input.policy, input.scope)
		}
	}
}

func TestRenderRouterOSTetheringTemplateIsScopedIdempotentAndRollbackSafe(t *testing.T) {
	template, err := RenderRouterOSTetheringTemplate(TetheringPolicy{Enabled: true, ExpectedClientTTL: 64}, "bridge-guest")
	if err != nil {
		t.Fatalf("RenderRouterOSTetheringTemplate() error = %v", err)
	}

	for _, want := range []string{
		"in-interface=bridge-guest", "hotspot=auth", "ttl=less-than:64",
		"NetCore:tethering:bridge-guest:mark", "NetCore:tethering:bridge-guest:drop",
		"action=mark-packet", "action=drop", "place-before=0",
	} {
		if !strings.Contains(template.Apply, want) {
			t.Errorf("apply template missing %q\n%s", want, template.Apply)
		}
	}
	if strings.Contains(template.Apply, "/ip firewall filter remove [find]") || strings.Contains(template.Rollback, "/ip firewall filter remove [find]") {
		t.Fatalf("templates must never target all firewall rules\napply:\n%s\nrollback:\n%s", template.Apply, template.Rollback)
	}
	for _, want := range []string{"comment=\"NetCore:tethering:bridge-guest:mark\"", "comment=\"NetCore:tethering:bridge-guest:drop\""} {
		if !strings.Contains(template.Rollback, want) {
			t.Errorf("rollback must target exactly %q\n%s", want, template.Rollback)
		}
	}
}
