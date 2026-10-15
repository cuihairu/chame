package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
)

// captureHandler records every attribute of every record it sees, so
// assertions can inspect exactly what passed through a handler.
type captureHandler struct {
	mu    sync.Mutex
	attrs []slog.Attr
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, rec slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec.Attrs(func(a slog.Attr) bool {
		h.attrs = append(h.attrs, a)
		return true
	})
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attrs = append(h.attrs, attrs...)
	return h
}

func (h *captureHandler) WithGroup(name string) slog.Handler { return h }

func (h *captureHandler) values(key string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, a := range h.attrs {
		if a.Key != key {
			continue
		}
		if a.Value.Kind() == slog.KindGroup {
			for _, inner := range a.Value.Group() {
				out = append(out, inner.Key+"="+inner.Value.String())
			}
			continue
		}
		out = append(out, a.Value.String())
	}
	return out
}

func (h *captureHandler) count(key, want string) int {
	n := 0
	for _, v := range h.values(key) {
		if v == want {
			n++
		}
	}
	return n
}

func TestRedactingHandlerScrubsSecretFields(t *testing.T) {
	cap := &captureHandler{}
	logger := slog.New(Redacting(cap))
	logger.Info("op",
		slog.String("password", "hunter2"),
		slog.String("private_key", "deadbeef"),
		slog.String("apiKey", "sk-123"),
		slog.String("seed phrase", "abandon abandon"),
		slog.String("authorization", "Bearer xyz"),
		slog.String("monkey", "keep"),
		slog.String("note", "hello"),
	)
	for _, key := range []string{"password", "private_key", "apiKey", "seed phrase", "authorization"} {
		if got := cap.count(key, "[redacted]"); got != 1 {
			t.Errorf("%s: redacted %d times, want 1", key, got)
		}
		for _, v := range cap.values(key) {
			if v != "[redacted]" {
				t.Errorf("%s leaked %q", key, v)
			}
		}
	}
	if got := cap.count("monkey", "keep"); got != 1 {
		t.Error("monkey must pass through")
	}
	if got := cap.count("note", "hello"); got != 1 {
		t.Error("note must pass through")
	}
}

func TestRedactingHandlerScrubsNestedGroups(t *testing.T) {
	cap := &captureHandler{}
	logger := slog.New(Redacting(cap))
	logger.Info("op", slog.Group("request",
		slog.String("path", "/v1/balance"),
		slog.String("token", "abc"),
	))
	want := []string{"path=/v1/balance", "token=[redacted]"}
	got := cap.values("request")
	if len(got) != len(want) {
		t.Fatalf("request group = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRedactingHandlerStripsURLCredentials(t *testing.T) {
	cap := &captureHandler{}
	logger := slog.New(Redacting(cap))
	logger.Info("endpoint",
		slog.String("rpc_url", "https://user:pass@example.com"),
		slog.String("dsn", "postgres://chame:chame@127.0.0.1:55442/chame_test?sslmode=disable"),
		slog.String("host", "mainnet-beta"),
	)
	if got := cap.count("rpc_url", "https://example.com"); got != 1 {
		t.Errorf("rpc_url = %v", cap.values("rpc_url"))
	}
	if got := cap.count("dsn", "postgres://127.0.0.1:55442/chame_test?sslmode=disable"); got != 1 {
		t.Errorf("dsn = %v", cap.values("dsn"))
	}
	if got := cap.count("host", "mainnet-beta"); got != 1 {
		t.Errorf("host = %v", cap.values("host"))
	}
}

// TestNewRedactsByDefault pins that the package's own logger constructor
// scrubs: an application building a logger from config must not have to
// remember to wrap it.
func TestNewRedactsByDefault(t *testing.T) {
	// New binds os.Stderr when it builds the handler, so the pipe has to
	// be in place first for the line to be readable back.
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	logger, err := New(DefaultOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	logger.Error("op", slog.String("password", "hunter2"), slog.String("network", "evm/mainnet/1"))
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	os.Stderr = old

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read: %v", err)
	}
	line := buf.String()
	if strings.TrimSpace(line) == "" {
		t.Fatal("nothing reached the handler: the test proves nothing")
	}
	if strings.Contains(line, "hunter2") {
		t.Errorf("package logger leaked the secret: %s", line)
	}
	if !strings.Contains(line, "[redacted]") {
		t.Errorf("secret field must be replaced, got %s", line)
	}
	// A non-secret field still gets through: redaction is not blanket.
	if !strings.Contains(line, "evm/mainnet/1") {
		t.Errorf("network field must survive redaction, got %s", line)
	}
}

func TestSplitKey(t *testing.T) {
	cases := []struct {
		key  string
		want []string
	}{
		{"private_key", []string{"private", "key"}},
		{"privateKey", []string{"private", "key"}},
		{"private key", []string{"private", "key"}},
		{"api_key", []string{"api", "key"}},
		{"apikey", []string{"apikey"}},
		{"PASSWORD", []string{"password"}},
		{"rpc_url", []string{"rpc", "url"}},
		{"monkey", []string{"monkey"}},
		{"", nil},
	}
	for _, tc := range cases {
		got := splitKey(tc.key)
		if len(got) != len(tc.want) {
			t.Errorf("splitKey(%q) = %v, want %v", tc.key, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitKey(%q) = %v, want %v", tc.key, got, tc.want)
				break
			}
		}
	}
}

func TestIsSecretKey(t *testing.T) {
	for _, key := range []string{"password", "private_key", "apiKey", "token", "secret", "seed", "mnemonic", "authorization", "auth", "private", "apikey", "key", "keys"} {
		if !isSecretKey(key) {
			t.Errorf("%q must be secret-bearing", key)
		}
	}
	for _, key := range []string{"monkey", "keyboard", "keeper", "note", "host", "module", "stage", "rpc_url", "network"} {
		if isSecretKey(key) {
			t.Errorf("%q must not be treated as secret", key)
		}
	}
}
