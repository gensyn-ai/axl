// Package metrics provides lightweight in-process counters, gauges, histograms,
// time-series ring buffers, per-peer stats, and a log ring for the node dashboard.
package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram

	peers   *PeerStats
	logRing *LogRing
	series  *SeriesStore
	runtime *RuntimeSampler
	startAt time.Time
}

func NewRegistry() *Registry {
	r := &Registry{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
		peers:      NewPeerStats(1024),
		logRing:    NewLogRing(1000),
		startAt:    time.Now(),
	}
	r.series = NewSeriesStore(3600, time.Second)
	r.runtime = NewRuntimeSampler(r)
	return r
}

func (r *Registry) Start() {
	r.series.Start(r)
	r.runtime.Start()
}

func (r *Registry) Stop() {
	r.series.Stop()
	r.runtime.Stop()
}

func (r *Registry) StartTime() time.Time { return r.startAt }
func (r *Registry) Peers() *PeerStats     { return r.peers }
func (r *Registry) Logs() *LogRing        { return r.logRing }
func (r *Registry) Series() *SeriesStore  { return r.series }

func (r *Registry) Counter(name string) *Counter {
	r.mu.RLock()
	c, ok := r.counters[name]
	r.mu.RUnlock()
	if ok {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	c = &Counter{}
	r.counters[name] = c
	return c
}

func (r *Registry) Gauge(name string) *Gauge {
	r.mu.RLock()
	g, ok := r.gauges[name]
	r.mu.RUnlock()
	if ok {
		return g
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gauges[name]; ok {
		return g
	}
	g = &Gauge{}
	r.gauges[name] = g
	return g
}

func (r *Registry) Histogram(name string) *Histogram {
	r.mu.RLock()
	h, ok := r.histograms[name]
	r.mu.RUnlock()
	if ok {
		return h
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histograms[name]; ok {
		return h
	}
	h = newHistogram()
	r.histograms[name] = h
	return h
}

// Snapshot returns a JSON-friendly view of all registered metrics.
type Snapshot struct {
	Counters   map[string]uint64           `json:"counters"`
	Gauges     map[string]int64            `json:"gauges"`
	Histograms map[string]HistogramSummary `json:"histograms"`
}

func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := Snapshot{
		Counters:   make(map[string]uint64, len(r.counters)),
		Gauges:     make(map[string]int64, len(r.gauges)),
		Histograms: make(map[string]HistogramSummary, len(r.histograms)),
	}
	for k, v := range r.counters {
		out.Counters[k] = v.Value()
	}
	for k, v := range r.gauges {
		out.Gauges[k] = v.Value()
	}
	for k, v := range r.histograms {
		out.Histograms[k] = v.Summary()
	}
	return out
}

// Counter is a monotonically increasing uint64.
type Counter struct{ v atomic.Uint64 }

func (c *Counter) Inc()              { c.v.Add(1) }
func (c *Counter) Add(n uint64)      { c.v.Add(n) }
func (c *Counter) Value() uint64     { return c.v.Load() }

// Gauge is an int64 that can move up or down.
type Gauge struct{ v atomic.Int64 }

func (g *Gauge) Set(n int64)  { g.v.Store(n) }
func (g *Gauge) Inc()         { g.v.Add(1) }
func (g *Gauge) Dec()         { g.v.Add(-1) }
func (g *Gauge) Add(n int64)  { g.v.Add(n) }
func (g *Gauge) Value() int64 { return g.v.Load() }

// Histogram with fixed exponential buckets in milliseconds.
// Suitable for latency observations.
var histBucketsMs = []float64{0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

type Histogram struct {
	mu      sync.Mutex
	buckets []uint64
	count   uint64
	sumMs   float64
	maxMs   float64
}

func newHistogram() *Histogram {
	return &Histogram{buckets: make([]uint64, len(histBucketsMs)+1)}
}

func (h *Histogram) Observe(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.sumMs += ms
	if ms > h.maxMs {
		h.maxMs = ms
	}
	idx := sort.SearchFloat64s(histBucketsMs, ms)
	h.buckets[idx]++
}

type HistogramSummary struct {
	Count uint64    `json:"count"`
	SumMs float64   `json:"sum_ms"`
	MaxMs float64   `json:"max_ms"`
	P50Ms float64   `json:"p50_ms"`
	P95Ms float64   `json:"p95_ms"`
	P99Ms float64   `json:"p99_ms"`
	Buckets []HistogramBucket `json:"buckets"`
}

type HistogramBucket struct {
	LeMs  float64 `json:"le_ms"` // upper bound; -1 means +Inf
	Count uint64  `json:"count"`
}

func (h *Histogram) Summary() HistogramSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := HistogramSummary{Count: h.count, SumMs: h.sumMs, MaxMs: h.maxMs}
	out.Buckets = make([]HistogramBucket, len(h.buckets))
	for i, c := range h.buckets {
		le := -1.0
		if i < len(histBucketsMs) {
			le = histBucketsMs[i]
		}
		out.Buckets[i] = HistogramBucket{LeMs: le, Count: c}
	}
	out.P50Ms = h.percentileLocked(0.50)
	out.P95Ms = h.percentileLocked(0.95)
	out.P99Ms = h.percentileLocked(0.99)
	return out
}

func (h *Histogram) percentileLocked(p float64) float64 {
	if h.count == 0 {
		return 0
	}
	target := uint64(float64(h.count) * p)
	if target == 0 {
		target = 1
	}
	var cum uint64
	for i, c := range h.buckets {
		cum += c
		if cum >= target {
			if i < len(histBucketsMs) {
				return histBucketsMs[i]
			}
			return h.maxMs
		}
	}
	return h.maxMs
}
