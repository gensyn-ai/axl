package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gensyn-ai/axl/internal/metrics"
)

// handleEvents streams snapshot + delta events to the dashboard via SSE.
//
// Event types:
//   - "snapshot": full SnapshotPayload, sent immediately on connect and every 5s.
//   - "counters": fast-moving counters/gauges + latest series tail, every 1s.
//   - "log":      one log line per event.
//   - "peers":    on detected peer-membership change.
func handleEvents(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		// Bootstrap: full snapshot.
		writeEvent(w, flusher, "snapshot", buildSnapshot(cfg))

		// Subscribe to logs.
		var logSub <-chan metrics.LogLine
		if cfg.Reg != nil {
			id, ch := cfg.Reg.Logs().Subscribe(256)
			logSub = ch
			defer cfg.Reg.Logs().Unsubscribe(id)
		}

		fastTick := time.NewTicker(time.Second)
		defer fastTick.Stop()
		fullTick := time.NewTicker(5 * time.Second)
		defer fullTick.Stop()

		var lastPeerSig string
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-fullTick.C:
				if !writeEvent(w, flusher, "snapshot", buildSnapshot(cfg)) {
					return
				}
			case <-fastTick.C:
				if !writeEvent(w, flusher, "counters", buildCountersDelta(cfg)) {
					return
				}
				sig, peers := peerSignature(cfg)
				if sig != lastPeerSig {
					lastPeerSig = sig
					if !writeEvent(w, flusher, "peers", peers) {
						return
					}
				}
			case entry, ok := <-logSub:
				if !ok {
					return
				}
				if !writeEvent(w, flusher, "log", entry) {
					return
				}
			}
		}
	}
}

type CountersDelta struct {
	At         time.Time            `json:"at"`
	Counters   map[string]uint64    `json:"counters"`
	Gauges     map[string]int64     `json:"gauges"`
	UptimeSecs float64              `json:"uptime_secs"`
}

func buildCountersDelta(cfg Config) CountersDelta {
	out := CountersDelta{At: time.Now()}
	if cfg.Reg != nil {
		snap := cfg.Reg.Snapshot()
		out.Counters = snap.Counters
		out.Gauges = snap.Gauges
	}
	out.UptimeSecs = time.Since(cfg.StartTime).Seconds()
	return out
}

func peerSignature(cfg Config) (string, TopologyView) {
	if cfg.YggCore == nil {
		return "", TopologyView{}
	}
	t := collectTopology(cfg.YggCore)
	sig := ""
	for _, p := range t.Peers {
		sig += p.PublicKey + ":" + boolStr(p.Up) + ";"
	}
	for _, n := range t.Tree {
		sig += n.PublicKey + ">" + n.Parent + ";"
	}
	return sig, t
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// writeEvent emits one SSE message; returns false if the underlying conn errored.
func writeEvent(w http.ResponseWriter, f http.Flusher, kind string, payload any) bool {
	buf, err := json.Marshal(payload)
	if err != nil {
		return true
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, buf); err != nil {
		return false
	}
	f.Flush()
	return true
}
