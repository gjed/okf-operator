package github

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

const redacted = "[REDACTED]"

// minSecretLen keeps short strings such as an empty PEM line from being
// registered and redacting unrelated text.
const minSecretLen = 8

// Secrets is the set of values that must never be logged. It is safe for
// concurrent use; tokens are added as they are minted.
type Secrets struct {
	mu   sync.RWMutex
	vals []string
}

// Add registers a secret. A PEM block is registered whole and line by line,
// so a partial dump of the key is redacted too.
func (s *Secrets) Add(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range append([]string{strings.TrimSpace(v)}, strings.Split(v, "\n")...) {
		c = strings.TrimSpace(c)
		if len(c) >= minSecretLen && !strings.HasPrefix(c, "-----") {
			s.vals = append(s.vals, c)
		}
	}
}

// Redact replaces every registered secret in str.
func (s *Secrets) Redact(str string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.vals {
		str = strings.ReplaceAll(str, v, redacted)
	}
	return str
}

// RedactingHandler wraps a slog.Handler and redacts registered secrets from
// messages and attribute values.
//
// SIMPLIFIED: attributes bound with WithAttrs are redacted when they are bound,
// so a secret registered later is not removed from them. Every secret this
// package handles is registered before any logger sees it. Upgrade path: keep
// the bound attributes and redact them per record.
type RedactingHandler struct {
	next    slog.Handler
	secrets *Secrets
}

// NewRedactingHandler returns a handler that redacts secrets before next.
func NewRedactingHandler(next slog.Handler, secrets *Secrets) *RedactingHandler {
	return &RedactingHandler{next: next, secrets: secrets}
}

// Enabled implements slog.Handler.
func (h *RedactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

// Handle implements slog.Handler.
func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, h.secrets.Redact(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

// WithAttrs implements slog.Handler.
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.attr(a)
	}
	return &RedactingHandler{next: h.next.WithAttrs(clean), secrets: h.secrets}
}

// WithGroup implements slog.Handler.
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{next: h.next.WithGroup(name), secrets: h.secrets}
}

func (h *RedactingHandler) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.secrets.Redact(v.String()))
	case slog.KindGroup:
		group := v.Group()
		clean := make([]any, len(group))
		for i, g := range group {
			clean[i] = h.attr(g)
		}
		return slog.Group(a.Key, clean...)
	case slog.KindAny:
		s := fmt.Sprint(v.Any())
		if r := h.secrets.Redact(s); r != s {
			return slog.String(a.Key, r)
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}
