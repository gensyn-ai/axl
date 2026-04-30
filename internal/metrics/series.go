package metrics

import (
	"sort"
	"sync"
	"time"
)

// SeriesStore holds a fixed-size ring buffer per named time-series.
// A sampler goroutine writes one value per series per tick.
type SeriesStore struct {
	mu       sync.RWMutex
	cap      int
	interval time.Duration
	series   map[string]*Series

	stopCh chan struct{}
	doneCh chan struct{}

	// Tracked counters: series name -> last counter value (for rate computation).
	rates map[string]rateSpec
	// Tracked gauges: series name -> snapshot func.
	gauges map[string]func() float64
}

type rateSpec struct {
	counter *Counter
	last    uint64
}

func NewSeriesStore(cap int, interval time.Duration) *SeriesStore {
	return &SeriesStore{
		cap:      cap,
		interval: interval,
		series:   make(map[string]*Series),
		rates:    make(map[string]rateSpec),
		gauges:   make(map[string]func() float64),
	}
}

// TrackRate registers a counter to be sampled as a per-second rate into a series.
func (s *SeriesStore) TrackRate(name string, c *Counter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rates[name] = rateSpec{counter: c, last: c.Value()}
	if _, ok := s.series[name]; !ok {
		s.series[name] = newSeries(s.cap)
	}
}

// TrackGauge registers a gauge (or any func returning a float) to be snapshotted.
func (s *SeriesStore) TrackGauge(name string, fn func() float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gauges[name] = fn
	if _, ok := s.series[name]; !ok {
		s.series[name] = newSeries(s.cap)
	}
}

func (s *SeriesStore) Start(_ *Registry) {
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	go s.run()
}

func (s *SeriesStore) Stop() {
	if s.stopCh == nil {
		return
	}
	close(s.stopCh)
	<-s.doneCh
}

func (s *SeriesStore) run() {
	defer close(s.doneCh)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case now := <-t.C:
			s.tick(now)
		}
	}
}

func (s *SeriesStore) tick(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dt := s.interval.Seconds()
	for name, spec := range s.rates {
		cur := spec.counter.Value()
		delta := cur - spec.last
		spec.last = cur
		s.rates[name] = spec
		s.series[name].push(now, float64(delta)/dt)
	}
	for name, fn := range s.gauges {
		s.series[name].push(now, fn())
	}
}

// SeriesSnapshot returns the contents of one series.
type SeriesSnapshot struct {
	Name   string    `json:"name"`
	Values []float64 `json:"values"`
	StartS int64     `json:"start_s"` // unix seconds for index 0
	StepS  float64   `json:"step_s"`
}

func (s *SeriesStore) Snapshot(name string) (SeriesSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ser, ok := s.series[name]
	if !ok {
		return SeriesSnapshot{}, false
	}
	vals, start := ser.values()
	return SeriesSnapshot{
		Name:   name,
		Values: vals,
		StartS: start.Unix(),
		StepS:  s.interval.Seconds(),
	}, true
}

func (s *SeriesStore) AllSnapshots() []SeriesSnapshot {
	s.mu.RLock()
	names := make([]string, 0, len(s.series))
	for k := range s.series {
		names = append(names, k)
	}
	s.mu.RUnlock()
	sort.Strings(names)
	out := make([]SeriesSnapshot, 0, len(names))
	for _, n := range names {
		if snap, ok := s.Snapshot(n); ok {
			out = append(out, snap)
		}
	}
	return out
}

// Series is a fixed-capacity ring buffer of float64 samples.
type Series struct {
	cap   int
	buf   []float64
	head  int
	count int
	first time.Time
}

func newSeries(cap int) *Series {
	return &Series{cap: cap, buf: make([]float64, cap)}
}

func (s *Series) push(at time.Time, v float64) {
	if s.count == 0 {
		s.first = at
	}
	s.buf[s.head] = v
	s.head = (s.head + 1) % s.cap
	if s.count < s.cap {
		s.count++
	} else {
		// Buffer full, advance the start time by one step
		s.first = s.first.Add(time.Second)
	}
}

func (s *Series) values() ([]float64, time.Time) {
	out := make([]float64, s.count)
	if s.count == 0 {
		return out, time.Time{}
	}
	start := (s.head - s.count + s.cap) % s.cap
	for i := 0; i < s.count; i++ {
		out[i] = s.buf[(start+i)%s.cap]
	}
	return out, s.first
}
