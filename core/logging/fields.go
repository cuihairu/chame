package logging

import "log/slog"

// Log field keys (spec §15.1). The set is fixed: framework code logs
// under these keys so collectors can rely on them, and no other key may
// carry identifying or secret-bearing values.
const (
	FieldApp           = "app"
	FieldModule        = "module"
	FieldNetwork       = "network"
	FieldOpID          = "op_id"
	FieldTxID          = "tx_id"
	FieldEventID       = "event_id"
	FieldStage         = "stage"
	FieldErrorCategory = "error_category"
	FieldRetryCount    = "retry_count"
)

// App returns the application identity field.
func App(name string) slog.Attr { return slog.String(FieldApp, name) }

// Module returns the module name field, the same annotation
// WithModule attaches.
func Module(name string) slog.Attr { return slog.String(FieldModule, name) }

// Network returns the logical network field, rendered by its String
// form so a line names the chain without naming an endpoint.
func Network(id string) slog.Attr { return slog.String(FieldNetwork, id) }

// OpID returns the operation identifier field.
func OpID(id string) slog.Attr { return slog.String(FieldOpID, id) }

// TxID returns the transaction identifier field: a transaction hash or
// signature, never a business identifier.
func TxID(id string) slog.Attr { return slog.String(FieldTxID, id) }

// EventID returns the event identifier field.
func EventID(id string) slog.Attr { return slog.String(FieldEventID, id) }

// Stage returns the pipeline stage field.
func Stage(name string) slog.Attr { return slog.String(FieldStage, name) }

// ErrorCategory returns the error classification field. Callers pass
// errors.CategoryOf(err) so a line carries the stable class and not the
// message.
func ErrorCategory(category string) slog.Attr { return slog.String(FieldErrorCategory, category) }

// RetryCount returns the retry attempt field.
func RetryCount(n int) slog.Attr { return slog.Int(FieldRetryCount, n) }
