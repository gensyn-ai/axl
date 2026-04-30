package metrics

import (
	"runtime"
	"sync"
	"time"
)

// RuntimeSampler periodically samples Go runtime metrics into registry gauges.
type RuntimeSampler struct {
	r      *Registry
	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once
}

func NewRuntimeSampler(r *Registry) *RuntimeSampler {
	return &RuntimeSampler{r: r}
}

func (s *RuntimeSampler) Start() {
	s.once.Do(func() {
		s.stopCh = make(chan struct{})
		s.doneCh = make(chan struct{})
		go s.run()
	})
}

func (s *RuntimeSampler) Stop() {
	if s.stopCh == nil {
		return
	}
	close(s.stopCh)
	<-s.doneCh
}

func (s *RuntimeSampler) run() {
	defer close(s.doneCh)

	goroutines := s.r.Gauge("runtime_goroutines")
	heap := s.r.Gauge("runtime_heap_bytes")
	stack := s.r.Gauge("runtime_stack_bytes")
	sys := s.r.Gauge("runtime_sys_bytes")
	gcCount := s.r.Gauge("runtime_gc_count")
	gcPauseLastNs := s.r.Gauge("runtime_gc_pause_last_ns")

	t := time.NewTicker(time.Second)
	defer t.Stop()
	var ms runtime.MemStats
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			runtime.ReadMemStats(&ms)
			goroutines.Set(int64(runtime.NumGoroutine()))
			heap.Set(int64(ms.HeapAlloc))
			stack.Set(int64(ms.StackSys))
			sys.Set(int64(ms.Sys))
			gcCount.Set(int64(ms.NumGC))
			if ms.NumGC > 0 {
				idx := (ms.NumGC + 255) % 256
				gcPauseLastNs.Set(int64(ms.PauseNs[idx]))
			}
		}
	}
}
