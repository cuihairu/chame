package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// logBuffer redirects a handler into a buffer for assertions.
func logBuffer(t *testing.T, opts Options) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	hopts := &slog.HandlerOptions{}
	switch opts.Level {
	case "debug":
		hopts.Level = slog.LevelDebug
	case "warn":
		hopts.Level = slog.LevelWarn
	case "error":
		hopts.Level = slog.LevelError
	default:
		hopts.Level = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(&buf, hopts)), &buf
}

func TestOptionsValidate(t *testing.T) {
	if err := DefaultOptions().Validate(); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	for _, bad := range []Options{
		{Level: "verbose", Format: "json"},
		{Level: "info", Format: "logfmt"},
		{Level: "", Format: ""},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
}

func TestWithModuleAnnotates(t *testing.T) {
	logger, buf := logBuffer(t, DefaultOptions())
	WithModule(logger, "greeter").Info("hello")
	if !strings.Contains(buf.String(), `"module":"greeter"`) {
		t.Errorf("log line missing module attr: %s", buf.String())
	}
}

func TestLevelFilter(t *testing.T) {
	logger, buf := logBuffer(t, Options{Level: "warn", Format: "json"})
	logger.Info("dropped")
	logger.Warn("kept")
	if strings.Contains(buf.String(), "dropped") {
		t.Error("info should be filtered at warn level")
	}
	if !strings.Contains(buf.String(), "kept") {
		t.Error("warn should pass at warn level")
	}
}
