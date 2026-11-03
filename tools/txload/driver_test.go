package txload

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/chame/tools/txload/devchain"
)

// selfHosted builds one mock chain with two HTTP endpoints and wires
// the driver's fault injectors, exactly as the command does.
func selfHosted(t *testing.T, faults devchain.Faults) *Driver {
	t.Helper()
	chain := devchain.NewChain(1337, 15*time.Millisecond)
	t.Cleanup(chain.Stop)
	cfg := Config{
		Endpoints:      []string{"http://ep-a", "http://ep-b"},
		KeyHex:         "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
		TPS:            30,
		Duration:       2 * time.Second,
		Concurrency:    4,
		TxTimeout:      3 * time.Second,
		RequestTimeout: 2 * time.Second,
	}
	d, err := NewDriver(cfg)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	for i, f := range []devchain.Faults{faults, faults} {
		node := chain.NewNode(f)
		srv := &http.Server{Handler: node}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
		url := "http://" + ln.Addr().String()
		d.cfg.Endpoints[i] = url
		d.AttachNode(url, node)
	}
	return d
}

func TestDriverRunsWithFaultsAndAccountsEveryTx(t *testing.T) {
	d := selfHosted(t, devchain.Faults{ErrorRate: 0.2, DropRate: 0.15, RevertRate: 0.15})
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Sent == 0 {
		t.Fatal("no transactions were sent")
	}
	if res.Submitted == 0 {
		t.Fatal("no transactions reached submission")
	}
	if res.Succeeded == 0 {
		t.Fatal("no transactions reached inclusion")
	}
	// Every sent transaction is accounted for exactly once: included,
	// reverted, failed, timed out, submit-failed, or still open.
	resolved := res.Succeeded + res.Reverted + res.Failed + res.TimedOut + res.SubmitFails
	if resolved > res.Sent {
		t.Errorf("resolved %d > sent %d", resolved, res.Sent)
	}
	if res.InflightEnd != res.Sent-resolved {
		t.Errorf("inflight = %d, want %d", res.InflightEnd, res.Sent-resolved)
	}
	// The failure injections must have bitten.
	if res.Reverted == 0 {
		t.Error("revert injection produced no reverted transactions")
	}
	if res.TimedOut == 0 && res.InflightEnd == 0 {
		t.Error("drop injection left no undecided transactions")
	}
	// Phase counts cover every tracked record exactly once.
	total := int64(0)
	for _, n := range res.PhaseCounts {
		total += n
	}
	if total != res.Sent {
		t.Errorf("phase counts sum %d != sent %d: %+v", total, res.Sent, res.PhaseCounts)
	}
	if _, ok := res.PhaseCounts["included"]; !ok {
		t.Errorf("no included phase in distribution: %+v", res.PhaseCounts)
	}
	// The recovery worker's transitions are counted.
	if len(res.Transitions) == 0 {
		t.Error("no state-machine transitions recorded")
	}
	// Endpoint health was probed for every endpoint.
	if len(res.EndpointHealth) != len(d.Config().Endpoints) {
		t.Errorf("endpoint health = %v, want an entry per endpoint", res.EndpointHealth)
	}
	// Metrics were published into the collector.
	if d.Metrics() == nil {
		t.Fatal("no metrics collector")
	}
}

func TestDriverRunCleanProducesOnlySuccess(t *testing.T) {
	d := selfHosted(t, devchain.Faults{})
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.SubmitFails != 0 || res.TimedOut != 0 || res.Reverted != 0 || res.Failed != 0 {
		t.Errorf("clean chain produced failures: %+v", res)
	}
	if res.Succeeded != res.Submitted {
		t.Errorf("succeeded %d != submitted %d on a clean chain", res.Succeeded, res.Submitted)
	}
	p50, p90, p99 := res.E2E.Percentiles()
	if p50 <= 0 || p90 < p50 || p99 < p90 {
		t.Errorf("percentiles not monotonic: %v/%v/%v", p50, p90, p99)
	}
}

func TestDriverConfigValidation(t *testing.T) {
	if _, err := NewDriver(Config{TPS: 1}); err == nil {
		t.Error("empty endpoint pool accepted")
	}
	if _, err := NewDriver(Config{Endpoints: []string{"http://x"}}); err == nil {
		t.Error("zero tps accepted")
	}
}

func TestHandlerServesDashboardAndMetrics(t *testing.T) {
	d := selfHosted(t, devchain.Faults{})
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = d.Run(context.Background())
	}()
	srv := &http.Server{Handler: d.Handler()}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	base := "http://" + ln.Addr().String()

	// The collector exists only once Run has started; poll until the
	// metrics surface answers.
	waitOK := func(path string, tries int) *http.Response {
		t.Helper()
		for i := 0; i < tries; i++ {
			resp, err := http.Get(base + path)
			if err == nil && resp.StatusCode == 200 {
				return resp
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s never answered 200", path)
		return nil
	}

	dash := waitOK("/", 100)
	_ = dash.Body.Close()

	resp := waitOK("/metrics", 100)
	defer func() { _ = resp.Body.Close() }()

	state := waitOK("/api/state", 100)
	_ = state.Body.Close()
	<-runDone
}
