package logging

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
)

// secretWords are the field names whose values must never reach a log
// line (spec §5.4, §15.1). Matching is word-based: the key is split on
// separators and case transitions, so "private_key", "privateKey" and
// "private key" all match while "monkey" does not.
var secretWords = map[string]bool{
	"password":      true,
	"passwd":        true,
	"token":         true,
	"secret":        true,
	"seed":          true,
	"mnemonic":      true,
	"auth":          true,
	"authorization": true,
	"key":           true,
	"keys":          true,
	"private":       true,
	"apikey":        true,
	"api_key":       true,
}

// redactingHandler scrubs records before they reach the wrapped handler:
// secret-bearing fields are replaced and URL credentials are stripped
// (spec §15.1: log lines carry no secret material).
type redactingHandler struct {
	inner slog.Handler
}

// Redacting wraps h so every record is scrubbed on the way out. New
// already wraps the handler it builds; use this for handlers built
// elsewhere.
func Redacting(h slog.Handler) slog.Handler {
	if h == nil {
		return nil
	}
	return redactingHandler{inner: h}
}

func (h redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return redactingHandler{inner: h.inner.WithAttrs(sanitizeAttrs(attrs))}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{inner: h.inner.WithGroup(name)}
}

func (h redactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	// Rebuild the record so sanitizing cannot disturb the original, then
	// hand the copy to the wrapped handler.
	out := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	var sanitized []slog.Attr
	rec.Attrs(func(a slog.Attr) bool {
		sanitized = append(sanitized, sanitizeAttr(a))
		return true
	})
	out.AddAttrs(sanitized...)
	return h.inner.Handle(ctx, out)
}

// sanitizeAttrs scrubs a list of attributes.
func sanitizeAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, sanitizeAttr(a))
	}
	return out
}

// sanitizeAttr scrubs one attribute. Group values are walked so nested
// fields are covered too.
func sanitizeAttr(a slog.Attr) slog.Attr {
	if isSecretKey(a.Key) {
		return slog.String(a.Key, "[redacted]")
	}
	a.Value = sanitizeValue(a.Value)
	return a
}

func sanitizeValue(v slog.Value) slog.Value {
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(redactString(v.String()))
	case slog.KindGroup:
		group := v.Group()
		for i := range group {
			group[i] = sanitizeAttr(group[i])
		}
		return slog.GroupValue(group...)
	default:
		return v
	}
}

// redactString strips userinfo from anything that parses as a URL.
func redactString(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	u.User = nil
	return u.String()
}

// isSecretKey reports whether a log field key names secret material.
func isSecretKey(key string) bool {
	for _, word := range splitKey(key) {
		if secretWords[word] {
			return true
		}
	}
	return false
}

// splitKey splits a field key into lowercase words: separators
// (".-_/: ") and lower-to-upper transitions both break words, so
// "private_key", "privateKey" and "private key" yield the same list.
func splitKey(key string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	prev := byte(0)
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			cur.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			if prev >= 'a' && prev <= 'z' {
				flush()
			}
			cur.WriteByte(c)
		default:
			flush()
		}
		prev = c
	}
	flush()
	return words
}
