package metrics

import (
	"sort"
	"sync"
	"time"
)

// PeerStats holds per-peer counters keyed by hex public key, with a bounded
// LRU eviction policy to prevent unbounded growth on high-churn networks.
type PeerStats struct {
	mu      sync.Mutex
	limit   int
	entries map[string]*PeerEntry
}

type PeerEntry struct {
	PeerID         string    `json:"peer_id"`
	BytesIn        uint64    `json:"bytes_in"`
	BytesOut       uint64    `json:"bytes_out"`
	MessagesIn     uint64    `json:"messages_in"`
	MessagesOut    uint64    `json:"messages_out"`
	ActiveConns    int       `json:"active_conns"`
	LastSeen       time.Time `json:"last_seen"`
	FirstSeen      time.Time `json:"first_seen"`
	LastError      string    `json:"last_error,omitempty"`
	LastErrorAt    time.Time `json:"last_error_at,omitempty"`
}

func NewPeerStats(limit int) *PeerStats {
	return &PeerStats{limit: limit, entries: make(map[string]*PeerEntry)}
}

func (p *PeerStats) get(id string) *PeerEntry {
	e, ok := p.entries[id]
	if !ok {
		now := time.Now()
		e = &PeerEntry{PeerID: id, FirstSeen: now, LastSeen: now}
		p.entries[id] = e
		p.evictIfNeeded()
	}
	return e
}

func (p *PeerStats) evictIfNeeded() {
	if len(p.entries) <= p.limit {
		return
	}
	// Evict oldest LastSeen (with no active conns).
	var oldest *PeerEntry
	for _, e := range p.entries {
		if e.ActiveConns > 0 {
			continue
		}
		if oldest == nil || e.LastSeen.Before(oldest.LastSeen) {
			oldest = e
		}
	}
	if oldest != nil {
		delete(p.entries, oldest.PeerID)
	}
}

func (p *PeerStats) RecordIn(id string, bytes int) {
	if id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.get(id)
	e.MessagesIn++
	e.BytesIn += uint64(bytes)
	e.LastSeen = time.Now()
}

func (p *PeerStats) RecordOut(id string, bytes int) {
	if id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.get(id)
	e.MessagesOut++
	e.BytesOut += uint64(bytes)
	e.LastSeen = time.Now()
}

func (p *PeerStats) ConnOpened(id string) {
	if id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.get(id)
	e.ActiveConns++
	e.LastSeen = time.Now()
}

func (p *PeerStats) ConnClosed(id string) {
	if id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.get(id)
	if e.ActiveConns > 0 {
		e.ActiveConns--
	}
	e.LastSeen = time.Now()
}

func (p *PeerStats) RecordError(id string, msg string) {
	if id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.get(id)
	e.LastError = msg
	e.LastErrorAt = time.Now()
}

func (p *PeerStats) Snapshot() []PeerEntry {
	p.mu.Lock()
	out := make([]PeerEntry, 0, len(p.entries))
	for _, e := range p.entries {
		out = append(out, *e)
	}
	p.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	return out
}

func (p *PeerStats) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}
