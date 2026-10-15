package integration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/cuihairu/chame/core/capability"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/health"
	"github.com/cuihairu/chame/core/logging"
	"github.com/cuihairu/chame/core/metrics"
	"github.com/cuihairu/chame/core/module"
	"github.com/cuihairu/chame/core/runtime"
	memmetrics "github.com/cuihairu/chame/infrastructure/observability/memory"
)

// chainModule stands in for a chain adapter module. It publishes the
// network capability, registers one dependency health check, and reports
// metrics and logs while starting — the three observability surfaces of
// spec §15 in the shape the real adapters use them.
type chainModule struct {
	name      string
	network   string
	provides  []capability.Name
	status    health.Status
	checkErr  error
	collector metrics.Collector
	logger    *slog.Logger
}

func (m *chainModule) Name() string { return m.name }

// info mirrors what a real adapter module declares (spec §4.4).
func (m *chainModule) info() module.Info { return module.Info{Provides: m.provides} }

func (m *chainModule) Register(r module.Registry) error {
	for _, c := range m.provides {
		if err := r.PutCapability(c, m.name); err != nil {
			return err
		}
	}
	return r.AddDependencyHealthCheck(m.name+".chain", []string{m.name},
		health.CheckFunc(func(context.Context) (health.Status, error) { return m.status, m.checkErr }))
}

func (m *chainModule) Start(context.Context) error {
	if m.collector != nil {
		labels := metrics.Labels{metrics.LabelModule: m.name, metrics.LabelNetwork: m.network}
		_ = m.collector.Counter(metrics.MetricRPCRequests).Add(labels, 1)
		_ = m.collector.Histogram(metrics.MetricRPCDuration).Observe(labels, 0.02)
		_ = m.collector.Gauge(metrics.MetricEndpointHealth).Set(labels, 1)
		_ = m.collector.Gauge(metrics.MetricRuntimeState).Set(metrics.Labels{metrics.LabelModule: m.name}, 1)
	}
	if m.logger != nil {
		decoded := cherrs.New(cherrs.EventDecodeFailure, m.name+".event.decode", "short log data")
		category, _ := cherrs.CategoryOf(decoded)
		logging.WithModule(m.logger, m.name).Info("chain module started",
			logging.Network(m.network),
			logging.Stage("start"),
			logging.OpID("op-1"),
			logging.ErrorCategory(string(category)),
			logging.RetryCount(1),
			// A signer configuration that must never reach a log line.
			slog.String("private_key", "deadbeefcafe"),
		)
	}
	return nil
}

func (m *chainModule) Stop(context.Context) error { return nil }

// startRuntime builds and starts a runtime with the given modules.
func startRuntime(t *testing.T, mods ...*chainModule) *runtime.Runtime {
	t.Helper()
	// The runtime's own lines are not under test here: keep them out of
	// the test output unless the case inspects them.
	opts := []runtime.Option{runtime.WithLogger(slog.New(logging.Redacting(slog.NewJSONHandler(io.Discard, nil))))}
	rt := runtime.New(opts...)
	for _, m := range mods {
		if err := rt.Register(m, m.info()); err != nil {
			t.Fatalf("register %s: %v", m.Name(), err)
		}
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Stop(context.Background()); err != nil {
			t.Errorf("stop: %v", err)
		}
	})
	return rt
}

// TestReadinessScopesUnusedOptionalModule is the spec §15.3 rule end to
// end: a Solana module whose cluster is unreachable makes the operator
// view unhealthy, and leaves a service that never uses Solana ready.
func TestReadinessScopesUnusedOptionalModule(t *testing.T) {
	evm := &chainModule{name: "evm", network: "evm/mainnet/1", status: health.Healthy,
		provides: []capability.Name{capability.Network}}
	solana := &chainModule{name: "solana", network: "solana/mainnet", status: health.Unhealthy,
		checkErr: errors.New("cluster unreachable")}
	rt := startRuntime(t, evm, solana)

	if got := rt.Health(context.Background()); got.Status != health.Unhealthy {
		t.Fatalf("operator health = %s, want unhealthy: %+v", got.Status, got.Results)
	}

	got := rt.Readiness(context.Background(), []string{"evm"})
	if got.Status != health.Healthy {
		t.Fatalf("evm-only readiness = %s, want healthy: %+v", got.Status, got.Results)
	}
	for _, res := range got.Results {
		if res.Name == "solana.chain" {
			t.Error("unused optional module must stay out of the readiness report")
		}
	}

	got = rt.Readiness(context.Background(), []string{"evm", "solana"})
	if got.Status != health.Unhealthy {
		t.Fatalf("readiness including solana = %s, want unhealthy", got.Status)
	}
	if len(got.Results) != 2 {
		t.Errorf("both dependency checks must run, got %+v", got.Results)
	}
}

// TestMetricsCollectorRecordsModuleSeries pins that a module reporting
// through the metrics contract lands in the collector with the spec §15.1
// label set, and that the cardinality rule holds at a real call site.
func TestMetricsCollectorRecordsModuleSeries(t *testing.T) {
	collector := memmetrics.New()
	mod := &chainModule{name: "evm", network: "evm/mainnet/1", status: health.Healthy, collector: collector}
	rt := startRuntime(t, mod)

	counters, histograms, gauges := collector.Snapshot()
	series := func(name string) metrics.Labels {
		for _, l := range counters {
			if l.Name == name {
				return l.Labels
			}
		}
		return nil
	}
	got := series(metrics.MetricRPCRequests)
	if got == nil || got[metrics.LabelNetwork] != "evm/mainnet/1" || got[metrics.LabelModule] != "evm" {
		t.Fatalf("rpc request series = %v, want the module and network labels", got)
	}
	if len(histograms) != 1 || histograms[0].Count != 1 {
		t.Errorf("latency histogram = %+v, want one observation", histograms)
	}
	if len(gauges) != 2 {
		t.Fatalf("gauges = %+v, want endpoint health and runtime state", gauges)
	}

	// §15.2: a transaction id is not a metric label.
	err := collector.Counter(metrics.MetricRPCRequests).
		Add(metrics.Labels{metrics.LabelModule: "evm", "tx_hash": "0xabc"}, 1)
	if err == nil {
		t.Error("a high-cardinality label must be refused at the call site")
	}

	// The runtime keeps serving: a refused measurement does not fail the
	// module, because metrics are reporting, not control flow.
	if rt.Health(context.Background()).Status != health.Healthy {
		t.Error("runtime must stay healthy")
	}
}

// TestFrameworkLoggerCarriesFieldsWithoutSecrets pins spec §15.1 on a real
// log line: the documented fields are present, the error is named by
// category rather than message, and no secret material appears.
func TestFrameworkLoggerCarriesFieldsWithoutSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(logging.Redacting(slog.NewJSONHandler(&buf, nil)))
	mod := &chainModule{name: "evm", network: "evm/mainnet/1", status: health.Healthy, logger: logger}
	startRuntime(t, mod)

	line := buf.String()
	if !strings.Contains(line, `"module":"evm"`) {
		t.Errorf("line missing module: %s", line)
	}
	for _, want := range []string{
		`"network":"evm/mainnet/1"`,
		`"stage":"start"`,
		`"op_id":"op-1"`,
		`"error_category":"event_decode_failure"`,
		`"retry_count":1`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("line missing %s: %s", want, line)
		}
	}
	if strings.Contains(line, "deadbeefcafe") {
		t.Errorf("log line leaked a private key: %s", line)
	}
	if !strings.Contains(line, "[redacted]") {
		t.Errorf("secret field should be replaced: %s", line)
	}
	if strings.Contains(line, "short log data") {
		t.Errorf("log line should name the category, not the message: %s", line)
	}
}
