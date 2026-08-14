package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// §46 — the forbidden-value list. If any of these reaches the output, the
// build must fail.
func TestRedaction_ForbiddenValuesNeverReachOutput(t *testing.T) {
	const canary = "SUPERSECRETVALUE"

	cases := []struct {
		name string
		attr slog.Attr
	}{
		{"password", slog.String("password", canary)},
		{"password_hash", slog.String("password_hash", canary)},
		{"router_password", slog.String("router_password", canary)},
		{"otp", slog.String("otp", canary)},
		{"payment secret", slog.String("payment_secret", canary)},
		{"api secret", slog.String("api_secret", canary)},
		{"radius secret", slog.String("radius_secret", canary)},
		{"session token", slog.String("session_token", canary)},
		{"authorization header", slog.String("Authorization", canary)},
		{"cookie", slog.String("Cookie", canary)},
		{"card pan", slog.String("card_pan", canary)},
		{"cvv", slog.String("cvv", canary)},
		{"private key", slog.String("private_key", canary)},
		{"uppercase key", slog.String("PASSWORD", canary)},
		{"mixed case", slog.String("Router_Secret", canary)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(&buf, Options{ServiceName: "t", Env: "test"})
			l.LogAttrs(context.Background(), slog.LevelInfo, "event", c.attr)

			if strings.Contains(buf.String(), canary) {
				t.Fatalf("secret leaked into log output: %s", buf.String())
			}
			if !strings.Contains(buf.String(), Redacted) {
				t.Fatalf("expected %s marker, got: %s", Redacted, buf.String())
			}
		})
	}
}

// A secret nested inside a group must still be redacted. This is the case
// naive key-matching misses.
func TestRedaction_NestedGroup(t *testing.T) {
	const canary = "NESTEDSECRET"
	var buf bytes.Buffer
	l := New(&buf, Options{ServiceName: "t", Env: "test"})

	l.LogAttrs(context.Background(), slog.LevelInfo, "webhook",
		slog.Group("gateway",
			slog.String("name", "paystack"),
			slog.String("api_key", canary),
			slog.Group("inner", slog.String("token", canary)),
		),
	)

	if strings.Contains(buf.String(), canary) {
		t.Fatalf("nested secret leaked: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "paystack") {
		t.Fatalf("non-sensitive sibling was wrongly dropped: %s", buf.String())
	}
}

// WithAttrs is the path a long-lived component logger takes. It must redact too.
func TestRedaction_WithAttrs(t *testing.T) {
	const canary = "PERSISTENTSECRET"
	var buf bytes.Buffer
	l := New(&buf, Options{ServiceName: "t", Env: "test"}).
		With(slog.String("db_password", canary), slog.String("component", "store"))

	l.Info("connected")
	if strings.Contains(buf.String(), canary) {
		t.Fatalf("secret leaked via With(): %s", buf.String())
	}
	if !strings.Contains(buf.String(), "store") {
		t.Fatalf("non-sensitive attr lost: %s", buf.String())
	}
}

// A type whose LogValue() returns a group containing a secret must not bypass
// redaction. This is why redact() resolves before inspecting.
type sneaky struct{ secret string }

func (s sneaky) LogValue() slog.Value {
	return slog.GroupValue(slog.String("token", s.secret), slog.String("kind", "sneaky"))
}

func TestRedaction_LogValuerCannotBypass(t *testing.T) {
	const canary = "VALUERSECRET"
	var buf bytes.Buffer
	l := New(&buf, Options{ServiceName: "t", Env: "test"})

	l.LogAttrs(context.Background(), slog.LevelInfo, "event", slog.Any("payload", sneaky{canary}))

	if strings.Contains(buf.String(), canary) {
		t.Fatalf("LogValuer bypassed redaction: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "sneaky") {
		t.Fatalf("expected non-sensitive field to survive: %s", buf.String())
	}
}

func TestRedaction_NonSensitiveUntouched(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Options{ServiceName: "t", Env: "test"})
	l.LogAttrs(context.Background(), slog.LevelInfo, "event",
		slog.String("customer_id", "cust-123"),
		slog.Int("status", 200),
		slog.String("event", "login"),
	)
	out := buf.String()
	for _, want := range []string{"cust-123", "200", "login"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output: %s", want, out)
		}
	}
	if strings.Contains(out, Redacted) {
		t.Errorf("nothing should have been redacted: %s", out)
	}
}

func TestIsSensitive(t *testing.T) {
	sensitive := []string{"password", "Password", "db_password", "api_key", "APIKEY",
		"session_token", "otp_code", "router_credential", "x_signature"}
	safe := []string{"customer_id", "tenant_id", "status", "duration_ms", "event",
		"request_id", "router_id", "consumed_bytes"}

	for _, k := range sensitive {
		if !IsSensitive(k) {
			t.Errorf("IsSensitive(%q) = false, want true", k)
		}
	}
	for _, k := range safe {
		if IsSensitive(k) {
			t.Errorf("IsSensitive(%q) = true, want false", k)
		}
	}
}

// §46 requires JSON output with the standard field set.
func TestOutputIsValidJSONWithServiceFields(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Options{ServiceName: "netcore-api", Env: "production"})
	l.Info("started")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if m["service"] != "netcore-api" {
		t.Errorf("service = %v, want netcore-api", m["service"])
	}
	if m["env"] != "production" {
		t.Errorf("env = %v, want production", m["env"])
	}
	if _, ok := m["time"]; !ok {
		t.Error("missing time field")
	}
}

// §45 — correlation fields flow from context into every line.
func TestFromContext_AttachesCorrelation(t *testing.T) {
	var buf bytes.Buffer
	base := New(&buf, Options{ServiceName: "t", Env: "test"})

	ctx := WithRequestID(context.Background(), "req-abc")
	ctx = WithTenantID(ctx, "tenant-1")
	ctx = WithUserID(ctx, "user-9")

	FromContext(ctx, base).Info("handled")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"request_id": "req-abc", "tenant_id": "tenant-1", "user_id": "user-9",
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %s", k, m[k], want)
		}
	}
}

func TestFromContext_EmptyContextAddsNothing(t *testing.T) {
	var buf bytes.Buffer
	base := New(&buf, Options{ServiceName: "t", Env: "test"})
	FromContext(context.Background(), base).Info("x")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"request_id", "tenant_id", "user_id"} {
		if _, ok := m[k]; ok {
			t.Errorf("unexpected %s in output", k)
		}
	}
}

func TestRequestID(t *testing.T) {
	if got := RequestID(context.Background()); got != "" {
		t.Errorf("empty context should yield \"\", got %q", got)
	}
	ctx := WithRequestID(context.Background(), "r1")
	if got := RequestID(ctx); got != "r1" {
		t.Errorf("got %q, want r1", got)
	}
}
