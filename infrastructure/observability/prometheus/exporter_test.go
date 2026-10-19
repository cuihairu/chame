package prometheus

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/chame/core/metrics"
	"github.com/cuihairu/chame/infrastructure/observability/memory"
)

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
