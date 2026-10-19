// Package prometheus exports the in-process collector using Prometheus text exposition.
// It uses the standard library only; Prometheus can scrape /metrics directly.
package prometheus

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/cuihairu/chame/infrastructure/observability/memory"
)

// Handler returns a scrape handler for c. Output is Prometheus text format 0.0.4.
func Handler(c *memory.Collector) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		counters, histograms, gauges := c.Snapshot()
		for _, s := range counters {
			fmt.Fprintf(w, "%s%s %s\n", s.Name, labels(s.Labels), number(s.Value))
		}
		for _, s := range gauges {
			fmt.Fprintf(w, "%s%s %s\n", s.Name, labels(s.Labels), number(s.Value))
		}
		for _, s := range histograms {
			for _, b := range s.Buckets {
				fmt.Fprintf(w, "%s_bucket%s %d\n", s.Name, labelsWithLE(s.Labels, strconv.FormatFloat(b.UpperBound, 'g', -1, 64)), b.Count)
			}
			fmt.Fprintf(w, "%s_bucket%s %d\n", s.Name, labelsWithLE(s.Labels, "+Inf"), s.Count)
			fmt.Fprintf(w, "%s_sum%s %s\n%s_count%s %d\n", s.Name, labels(s.Labels), number(s.Sum), s.Name, labels(s.Labels), s.Count)
		}
	})
}

func number(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
func labels(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(m[k])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
func labelsWithLE(m map[string]string, le string) string {
	cp := make(map[string]string, len(m)+1)
	for k, v := range m {
		cp[k] = v
	}
	cp["le"] = le
	return labels(cp)
}
