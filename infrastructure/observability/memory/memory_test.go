package memory

import (
	"math"
	"sync"
	"testing"

	"github.com/cuihairu/chame/core/metrics"
)

func TestCounterAndGauge(t *testing.T) {
	c := New()
	labels := metrics.Labels{metrics.LabelModule: "evm", metrics.LabelNetwork: "evm/mainnet/1"}
	if err := c.Counter(metrics.MetricRPCRequests).Add(labels, 1); err != nil {
		t.Fatalf("counter: %v", err)
	}
	if err := c.Counter(metrics.MetricRPCRequests).Add(labels, 2); err != nil {
		t.Fatalf("counter: %v", err)
	}
	if err := c.Gauge(metrics.MetricRuntimeState).Set(labels, 1); err != nil {
		t.Fatalf("gauge: %v", err)
	}
	counters, _, gauges := c.Snapshot()
	if len(counters) != 1 || counters[0].Value != 3 {
		t.Errorf("counters = %+v", counters)
	}
	if len(gauges) != 1 || gauges[0].Value != 1 {
		t.Errorf("gauges = %+v", gauges)
	}
}

// TestCardinalityGuard pins that a metric cannot grow without bound: the
// series limit rejects new label sets rather than letting a runaway label
// create one series per request.
func TestCardinalityGuard(t *testing.T) {
	c := New().WithMaxSeries(2)
	counter := c.Counter(metrics.MetricRPCRequests)
	if err := counter.Add(metrics.Labels{metrics.LabelMethod: "a"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := counter.Add(metrics.Labels{metrics.LabelMethod: "b"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := counter.Add(metrics.Labels{metrics.LabelMethod: "c"}, 1); err == nil {
		t.Fatal("third series must be rejected")
	} else {
		t.Logf("third series rejected: %v", err)
	}
	if got := c.SeriesCount(metrics.MetricRPCRequests); got != 2 {
		t.Errorf("series = %d, want 2", got)
	}
	// Existing series still accept updates.
	if err := counter.Add(metrics.Labels{metrics.LabelMethod: "a"}, 1); err != nil {
		t.Fatal(err)
	}
}

// TestRejectsBadLabels pins that the collector enforces the label
// contract, not just the application.
func TestRejectsBadLabels(t *testing.T) {
	c := New()
	bad := metrics.Labels{metrics.LabelModule: "evm", "player_id": "p123"}
	if err := c.Counter(metrics.MetricRPCRequests).Add(bad, 1); err == nil {
		t.Fatal("player id must be rejected")
	}
	if err := c.Gauge(metrics.MetricRuntimeState).Set(bad, 1); err == nil {
		t.Fatal("player id must be rejected")
	}
	if err := c.Histogram(metrics.MetricRPCDuration).Observe(bad, 0.1); err == nil {
		t.Fatal("player id must be rejected")
	}
	if _, _, gauges := c.Snapshot(); len(gauges) != 0 {
		t.Error("rejected writes must not create series")
	}
}

func TestHistogram(t *testing.T) {
	c := New()
	labels := metrics.Labels{metrics.LabelModule: "evm"}
	h := c.Histogram(metrics.MetricRPCDuration)
	for _, v := range []float64{0.01, 0.2, 3} {
		if err := h.Observe(labels, v); err != nil {
			t.Fatal(err)
		}
	}
	_, histograms, _ := c.Snapshot()
	if len(histograms) != 1 {
		t.Fatalf("histograms = %d, want 1", len(histograms))
	}
	got := histograms[0]
	if want := 0.01 + 0.2 + 3; got.Count != 3 || math.Abs(got.Sum-want) > 1e-9 {
		t.Errorf("count=%d sum=%v, want count=3 sum=%v", got.Count, got.Sum, want)
	}
	// Buckets are cumulative: 0.01 lands in the 0.05 bucket, 0.2 in 0.25,
	// 3 in the 5 bucket.
	var at005, at025, at5 uint64
	for _, b := range got.Buckets {
		switch b.UpperBound {
		case 0.05:
			at005 = b.Count
		case 0.25:
			at025 = b.Count
		case 5:
			at5 = b.Count
		}
	}
	if at005 != 1 || at025 != 2 || at5 != 3 {
		t.Errorf("buckets = %v, want 1/2/3", got.Buckets)
	}
}

// TestSnapshotSorts pins a stable order for test assertions and for a
// future exporter.
func TestSnapshotSorts(t *testing.T) {
	c := New()
	one := metrics.Labels{metrics.LabelModule: "evm"}
	two := metrics.Labels{metrics.LabelModule: "solana"}
	for _, l := range []metrics.Labels{two, one} {
		if err := c.Counter(metrics.MetricRPCRequests).Add(l, 1); err != nil {
			t.Fatal(err)
		}
	}
	counters, _, _ := c.Snapshot()
	if len(counters) != 2 || counters[0].Labels[metrics.LabelModule] != "evm" || counters[1].Labels[metrics.LabelModule] != "solana" {
		t.Errorf("snapshot order = %+v", counters)
	}
}

// TestConcurrentUse pins that the collector is safe under concurrent
// writers (spec §16.1: concurrent calls).
func TestConcurrentUse(t *testing.T) {
	c := New()
	labels := metrics.Labels{metrics.LabelModule: "evm"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := c.Counter(metrics.MetricRPCRequests).Add(labels, 1); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	counters, _, _ := c.Snapshot()
	if len(counters) != 1 || counters[0].Value != 800 {
		t.Errorf("counter = %+v, want one series at 800", counters)
	}
}

func TestWithMaxSeriesDisables(t *testing.T) {
	c := New().WithMaxSeries(0)
	if err := c.Counter(metrics.MetricRPCRequests).Add(metrics.Labels{}, 1); err == nil {
		t.Error("disabled metric must reject writes")
	}
}
