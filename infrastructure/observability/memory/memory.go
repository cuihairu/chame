// Package memory implements the metrics contract with an in-process
// collector (spec §15.2, ADR-0007). It is the reference implementation the
// runtime and examples can use without an external backend; the official
// Prometheus exporter is deferred to the production infrastructure phase
// and lands beside this package.
package memory

import (
	"fmt"
	"sort"
	"sync"

	"github.com/cuihairu/chame/core/metrics"
)

// DefaultMaxSeries bounds how many distinct label sets one metric may
// carry before new ones are rejected. The bound is the structural
// cardinality guard: a runaway label cannot grow the series count without
// limit (spec §15.2).
const DefaultMaxSeries = 500

// DefaultBuckets are the histogram bucket upper bounds, in seconds.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Collector is an in-process metrics collector. It is safe for
// concurrent use.
type Collector struct {
	mu         sync.Mutex
	counters   map[string]map[string]*counterSeries
	histograms map[string]map[string]*histogramSeries
	gauges     map[string]map[string]*gaugeSeries
	maxSeries  int
}

// counterSeries is one counter series: its labels and its value.
type counterSeries struct {
	labels metrics.Labels
	value  float64
}

// gaugeSeries is one gauge series: its labels and its value.
type gaugeSeries struct {
	labels metrics.Labels
	value  float64
}

// histogramSeries is one histogram series: its labels and its buckets.
type histogramSeries struct {
	labels metrics.Labels
	counts []uint64
	count  uint64
	sum    float64
}

// New returns an empty collector with the default series bound.
func New() *Collector {
	return &Collector{
		counters:   make(map[string]map[string]*counterSeries),
		histograms: make(map[string]map[string]*histogramSeries),
		gauges:     make(map[string]map[string]*gaugeSeries),
		maxSeries:  DefaultMaxSeries,
	}
}

// WithMaxSeries sets how many distinct label sets one metric may carry.
// A value below one disables the metric.
func (c *Collector) WithMaxSeries(n int) *Collector {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxSeries = n
	return c
}

// SeriesCount reports how many distinct label sets a metric currently
// carries. It exists so tests and diagnostics can watch the cardinality
// guard.
func (c *Collector) SeriesCount(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.counters[name]; ok {
		return len(s)
	}
	if s, ok := c.histograms[name]; ok {
		return len(s)
	}
	if s, ok := c.gauges[name]; ok {
		return len(s)
	}
	return 0
}

// Counter resolves the named counter.
func (c *Collector) Counter(name string) metrics.Counter {
	return &collectorCounter{collector: c, name: name}
}

// Histogram resolves the named histogram.
func (c *Collector) Histogram(name string) metrics.Histogram {
	return &collectorHistogram{collector: c, name: name}
}

// Gauge resolves the named gauge.
func (c *Collector) Gauge(name string) metrics.Gauge {
	return &collectorGauge{collector: c, name: name}
}

// checkSeries enforces the cardinality bound for one metric's series
// table. It is called before a new series is created.
func checkSeries[V any](c *Collector, name string, table map[string]V) error {
	if c.maxSeries < 1 {
		return fmt.Errorf("metrics: metric %q is disabled", name)
	}
	if len(table) >= c.maxSeries {
		return fmt.Errorf("metrics: series limit %d reached for %q", c.maxSeries, name)
	}
	return nil
}

type collectorCounter struct {
	collector *Collector
	name      string
}

func (c *collectorCounter) Add(labels metrics.Labels, delta float64) error {
	c.collector.mu.Lock()
	defer c.collector.mu.Unlock()
	if err := metrics.ValidateLabels(labels); err != nil {
		return err
	}
	series, ok := c.collector.counters[c.name]
	if !ok {
		series = make(map[string]*counterSeries)
		c.collector.counters[c.name] = series
	}
	key := metrics.Key(labels)
	entry, exists := series[key]
	if !exists {
		if err := checkSeries(c.collector, c.name, series); err != nil {
			return err
		}
		entry = &counterSeries{labels: labels}
		series[key] = entry
	}
	entry.value += delta
	return nil
}

type collectorHistogram struct {
	collector *Collector
	name      string
}

func (c *collectorHistogram) Observe(labels metrics.Labels, value float64) error {
	c.collector.mu.Lock()
	defer c.collector.mu.Unlock()
	if err := metrics.ValidateLabels(labels); err != nil {
		return err
	}
	series, ok := c.collector.histograms[c.name]
	if !ok {
		series = make(map[string]*histogramSeries)
		c.collector.histograms[c.name] = series
	}
	key := metrics.Key(labels)
	entry, exists := series[key]
	if !exists {
		if err := checkSeries(c.collector, c.name, series); err != nil {
			return err
		}
		entry = &histogramSeries{labels: labels, counts: make([]uint64, len(DefaultBuckets))}
		series[key] = entry
	}
	entry.count++
	entry.sum += value
	for i, upper := range DefaultBuckets {
		if value <= upper {
			entry.counts[i]++
		}
	}
	return nil
}

type collectorGauge struct {
	collector *Collector
	name      string
}

func (c *collectorGauge) Set(labels metrics.Labels, value float64) error {
	c.collector.mu.Lock()
	defer c.collector.mu.Unlock()
	if err := metrics.ValidateLabels(labels); err != nil {
		return err
	}
	series, ok := c.collector.gauges[c.name]
	if !ok {
		series = make(map[string]*gaugeSeries)
		c.collector.gauges[c.name] = series
	}
	key := metrics.Key(labels)
	entry, exists := series[key]
	if !exists {
		if err := checkSeries(c.collector, c.name, series); err != nil {
			return err
		}
		entry = &gaugeSeries{labels: labels}
		series[key] = entry
	}
	entry.value = value
	return nil
}

// Samples is one collected counter or gauge series.
type Samples struct {
	Name   string
	Labels metrics.Labels
	Value  float64
}

// HistogramSamples is one collected histogram series.
type HistogramSamples struct {
	Name    string
	Labels  metrics.Labels
	Count   uint64
	Sum     float64
	Buckets []BucketSamples
}

// BucketSamples is one histogram bucket.
type BucketSamples struct {
	UpperBound float64
	Count      uint64
}

// Snapshot returns every collected series, sorted by name and label key.
// It exists for tests and for a future exporter to read.
func (c *Collector) Snapshot() (counters []Samples, histograms []HistogramSamples, gauges []Samples) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, series := range c.counters {
		for _, entry := range series {
			counters = append(counters, Samples{Name: name, Labels: entry.labels, Value: entry.value})
		}
	}
	sort.Slice(counters, func(i, j int) bool {
		if counters[i].Name != counters[j].Name {
			return counters[i].Name < counters[j].Name
		}
		return metrics.Key(counters[i].Labels) < metrics.Key(counters[j].Labels)
	})
	for name, series := range c.histograms {
		for _, entry := range series {
			sample := HistogramSamples{Name: name, Labels: entry.labels, Count: entry.count, Sum: entry.sum}
			for i, upper := range DefaultBuckets {
				sample.Buckets = append(sample.Buckets, BucketSamples{UpperBound: upper, Count: entry.counts[i]})
			}
			histograms = append(histograms, sample)
		}
	}
	sort.Slice(histograms, func(i, j int) bool {
		if histograms[i].Name != histograms[j].Name {
			return histograms[i].Name < histograms[j].Name
		}
		return metrics.Key(histograms[i].Labels) < metrics.Key(histograms[j].Labels)
	})
	for name, series := range c.gauges {
		for _, entry := range series {
			gauges = append(gauges, Samples{Name: name, Labels: entry.labels, Value: entry.value})
		}
	}
	sort.Slice(gauges, func(i, j int) bool {
		if gauges[i].Name != gauges[j].Name {
			return gauges[i].Name < gauges[j].Name
		}
		return metrics.Key(gauges[i].Labels) < metrics.Key(gauges[j].Labels)
	})
	return counters, histograms, gauges
}
