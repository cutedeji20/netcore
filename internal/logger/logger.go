// Package logger provides structured JSON logging with mandatory redaction.
//
// Spec: BUILD.md §46.
//
// The redaction is a Handler, not a convention. §46 lists values that must
// never be logged; relying on every future caller to remember that list is how
// a session token ends up in Loki. Here, a forbidden key is redacted no matter
// how it is passed — including nested inside a slog.Group or a LogValuer.
package logger

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// Redacted is what replaces a sensitive value.
const Redacted = "[REDACTED]"

// sensitiveKeys are matched case-insensitively as substrings of the attribute
// key. Substring matching is deliberate: it catches router_password,
// db_password, and password_hash without enumerating every variant.
//
// Derived from §46 plus the v1.1 additions.
var sensitiveKeys = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"otp",
	"api_key",
	"apikey",
	"authorization",
	"cookie",
	"session_id",
	"private_key",
	"credential",
	"card",
	"cvv",
	"pan",
	"signature",
	"secret_ref_value",
}

// IsSensitive reports whether an attribute key must be redacted.
func IsSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// RedactingHandler wraps a slog.Handler and redacts sensitive attributes.
type RedactingHandler struct{ inner slog.Handler }

// NewRedactingHandler wraps h.
func NewRedactingHandler(h slog.Handler) *RedactingHandler {
	return &RedactingHandler{inner: h}
}

func (h *RedactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	clone := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clone.AddAttrs(redact(a))
		return true
	})
	return h.inner.Handle(ctx, clone)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redact(a)
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(out)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name)}
}

// redact rewrites a single attribute, recursing into groups so a secret nested
// in slog.Group("payment", "secret", v) is still caught.
func redact(a slog.Attr) slog.Attr {
	// Resolve LogValuer first: otherwise a type whose LogValue() returns a
	// group containing a secret bypasses redaction entirely.
	a.Value = a.Value.Resolve()

	if IsSensitive(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	if a.Value.Kind() == slog.KindGroup {
		src := a.Value.Group()
		out := make([]slog.Attr, len(src))
		for i, g := range src {
			out[i] = redact(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	}
	return a
}

// Options configures the base logger.
type Options struct {
	Level       slog.Level
	ServiceName string
	Env         string
	AddSource   bool
}

// New builds a JSON logger with redaction enabled and the standard §46 fields
// attached.
func New(w io.Writer, o Options) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     o.Level,
		AddSource: o.AddSource,
	})
	h := NewRedactingHandler(base)
	return slog.New(h).With(
		slog.String("service", o.ServiceName),
		slog.String("env", o.Env),
	)
}

// ---------------------------------------------------------------------------
// Correlation — §45
// ---------------------------------------------------------------------------

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxTenantID
	ctxUserID
)

// WithRequestID stores a request ID for later log enrichment.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxRequestID, id)
}

// WithTenantID stores the authenticated tenant. §10: this comes from the
// authenticated principal, never from a client-supplied value.
func WithTenantID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxTenantID, id)
}

// WithUserID stores the authenticated user.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxUserID, id)
}

// RequestID returns the correlation ID, or "" if unset.
func RequestID(ctx context.Context) string { return strFromCtx(ctx, ctxRequestID) }

// FromContext returns a logger enriched with whatever correlation fields are
// present, per §46.
func FromContext(ctx context.Context, l *slog.Logger) *slog.Logger {
	if l == nil {
		l = slog.Default()
	}
	if v := strFromCtx(ctx, ctxRequestID); v != "" {
		l = l.With(slog.String("request_id", v))
	}
	if v := strFromCtx(ctx, ctxTenantID); v != "" {
		l = l.With(slog.String("tenant_id", v))
	}
	if v := strFromCtx(ctx, ctxUserID); v != "" {
		l = l.With(slog.String("user_id", v))
	}
	return l
}

func strFromCtx(ctx context.Context, k ctxKey) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(k).(string); ok {
		return v
	}
	return ""
}
