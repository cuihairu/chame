// Package logging defines the framework logging contract (spec §15.1).
// It is built on the standard library log/slog facade. Loggers are
// injected by the composition root; the package has no global logger and
// framework code must never log secrets (spec §5.4, §12).
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Options mirror the logging section of the application config
// (spec §5.3). Level is one of debug, info, warn, error; Format is one of
// json, text.
type Options struct {
	Level  string
	Format string
}

// DefaultOptions are used when the application config does not say.
func DefaultOptions() Options { return Options{Level: "info", Format: "json"} }

// Validate checks the options against the supported values.
func (o Options) Validate() error {
	switch o.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging: unsupported level %q", o.Level)
	}
	switch o.Format {
	case "json", "text":
	default:
		return fmt.Errorf("logging: unsupported format %q", o.Format)
	}
	return nil
}

// New builds a slog.Logger writing to stderr. There is no package-level
// logger: callers hold and pass the returned value explicitly.
func New(opts Options) (*slog.Logger, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	var level slog.Level
	switch strings.ToLower(opts.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	hopts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if opts.Format == "text" {
		h = slog.NewTextHandler(os.Stderr, hopts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, hopts)
	}
	// The framework logger redacts by default: a logger that could
	// leak credentials would defeat the spec's field rules (spec §15.1).
	return slog.New(Redacting(h)), nil
}

// WithModule returns a logger annotated with the module name, so log
// lines from different modules stay attributable (spec §15.1).
func WithModule(l *slog.Logger, module string) *slog.Logger {
	return l.With(Module(module))
}
