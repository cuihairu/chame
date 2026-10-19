package prometheus

import (
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cuihairu/chame/core/metrics"
	"github.com/cuihairu/chame/infrastructure/observability/memory"
)

type failingResponseWriter struct {
	*httptest.ResponseRecorder
	failAt int
	writes int
}

func (w *failingResponseWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= w.failAt {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(p)
}

func TestHandlerStopsAfterWriteFailure(t *testing.T) {
	c := memory.New()
	if err := c.Counter(TransactionsSubmitted).Add(nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Gauge("active_connections").Set(nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Histogram(TransactionConfirmationSeconds).Observe(nil, 0.5); err != nil {
		t.Fatal(err)
	}
	// Counter, gauge, finite buckets, +Inf bucket, and combined sum/count.
	for failAt := 1; failAt <= len(memory.DefaultBuckets)+4; failAt++ {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			w := &failingResponseWriter{ResponseRecorder: httptest.NewRecorder(), failAt: failAt}
			Handler(c).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
			if w.writes != failAt {
				t.Fatalf("write attempts = %d, want %d", w.writes, failAt)
			}
		})
	}
}

func TestHandlerExportsPrometheusText(t *testing.T) {
	c := memory.New()
	_ = c.Counter(TransactionsSubmitted).Add(metrics.Labels{"network": "fake"}, 2)
	_ = c.Histogram(TransactionConfirmationSeconds).Observe(metrics.Labels{"network": "fake"}, 0.5)
	r := httptest.NewRecorder()
	Handler(c).ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(r.Header().Get("Content-Type"), "text/plain; version=0.0.4") || !strings.Contains(r.Body.String(), "transactions_submitted_total{network=\"fake\"} 2") || !strings.Contains(r.Body.String(), "transaction_confirmation_seconds_count{network=\"fake\"} 1") {
		t.Fatalf("unexpected scrape: %s", r.Body.String())
	}
}

func TestRecorderMetricsNeverExposeIdempotencyKey(t *testing.T) {
	c := memory.New()
	r := NewRecorder(c)
	if err := r.TransactionSubmitted("fake"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	Handler(c).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if strings.Contains(w.Body.String(), "idempotency") || strings.Contains(w.Body.String(), "player") || strings.Contains(w.Body.String(), "transaction_id") {
		t.Fatalf("high-cardinality correlation leaked into metrics: %s", w.Body.String())
	}
}
