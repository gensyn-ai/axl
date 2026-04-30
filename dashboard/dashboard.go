// Package dashboard serves the in-process node dashboard:
// embedded SPA, JSON snapshot endpoint, SSE event stream, log tail.
package dashboard

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/gensyn-ai/axl/api"
	"github.com/gensyn-ai/axl/internal/metrics"
)

//go:embed web
var webFS embed.FS

// Config carries everything the dashboard needs to render snapshots.
type Config struct {
	Reg          *metrics.Registry
	YggCore      api.TopologyProvider
	BuildInfo    BuildInfo
	NodeConfig   NodeConfigView
	StartTime    time.Time
}

// BuildInfo describes the running binary. Populated via -ldflags at build time.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// NodeConfigView is the subset of node config we surface in the dashboard.
type NodeConfigView struct {
	TCPPort         int    `json:"tcp_port"`
	APIPort         int    `json:"api_port"`
	BridgeAddr      string `json:"bridge_addr"`
	McpRouterAddr   string `json:"mcp_router_addr"`
	A2AAddr         string `json:"a2a_addr"`
	MaxMessageSize  uint32 `json:"max_message_size"`
	MaxConcConns    int    `json:"max_concurrent_conns"`
	ConnReadTimeout string `json:"conn_read_timeout"`
	ConnIdleTimeout string `json:"conn_idle_timeout"`
}

// Mount registers all dashboard routes onto mux.
func Mount(mux *http.ServeMux, cfg Config) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/dashboard/static/", http.StripPrefix("/dashboard/static/", fileServer))
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/", http.StatusFound)
	})
	mux.HandleFunc("/dashboard/", func(w http.ResponseWriter, r *http.Request) {
		// SPA: always serve index.html for any /dashboard/ subroute.
		data, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "dashboard not embedded", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'")
		w.Write(data)
	})
	mux.HandleFunc("/dashboard/api/snapshot", handleSnapshot(cfg))
	mux.HandleFunc("/dashboard/api/series", handleSeries(cfg))
	mux.HandleFunc("/dashboard/api/peers", handlePeers(cfg))
	mux.HandleFunc("/dashboard/api/logs", handleLogs(cfg))
	mux.HandleFunc("/dashboard/api/topology", handleTopology(cfg))
	mux.HandleFunc("/dashboard/api/events", handleEvents(cfg))
}

// SnapshotPayload is the bootstrap state for a freshly opened dashboard tab.
type SnapshotPayload struct {
	Identity   IdentityInfo            `json:"identity"`
	Build      BuildInfo               `json:"build"`
	Config     NodeConfigView          `json:"config"`
	Metrics    metrics.Snapshot        `json:"metrics"`
	Series     []metrics.SeriesSnapshot `json:"series"`
	Peers      []metrics.PeerEntry     `json:"peers"`
	Topology   TopologyView            `json:"topology"`
	Now        time.Time               `json:"now"`
	UptimeSecs float64                 `json:"uptime_secs"`
}

type IdentityInfo struct {
	IPv6      string `json:"ipv6"`
	PublicKey string `json:"public_key"`
	Hostname  string `json:"hostname"`
}

type TopologyView struct {
	Self  string             `json:"self"`
	Peers []api.PeerInfo     `json:"peers"`
	Tree  []api.TreeInfo     `json:"tree"`
}

func handleSnapshot(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, buildSnapshot(cfg))
	}
}

func buildSnapshot(cfg Config) SnapshotPayload {
	out := SnapshotPayload{
		Build:   cfg.BuildInfo,
		Config:  cfg.NodeConfig,
		Now:     time.Now(),
		UptimeSecs: time.Since(cfg.StartTime).Seconds(),
	}
	if cfg.Reg != nil {
		out.Metrics = cfg.Reg.Snapshot()
		out.Series = cfg.Reg.Series().AllSnapshots()
		out.Peers = cfg.Reg.Peers().Snapshot()
	}
	if cfg.YggCore != nil {
		out.Identity = IdentityInfo{
			IPv6:      cfg.YggCore.Address().String(),
			PublicKey: hex.EncodeToString(cfg.YggCore.PublicKey()),
			Hostname:  hostname(),
		}
		out.Topology = collectTopology(cfg.YggCore)
	}
	return out
}

func handleSeries(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Reg == nil {
			writeJSON(w, []metrics.SeriesSnapshot{})
			return
		}
		writeJSON(w, cfg.Reg.Series().AllSnapshots())
	}
}

func handlePeers(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Reg == nil {
			writeJSON(w, []metrics.PeerEntry{})
			return
		}
		writeJSON(w, cfg.Reg.Peers().Snapshot())
	}
}

func handleLogs(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 200
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 5000 {
				limit = n
			}
		}
		if cfg.Reg == nil {
			writeJSON(w, []metrics.LogLine{})
			return
		}
		writeJSON(w, cfg.Reg.Logs().Snapshot(limit))
	}
}

func handleTopology(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.YggCore == nil {
			writeJSON(w, TopologyView{})
			return
		}
		writeJSON(w, collectTopology(cfg.YggCore))
	}
}

func collectTopology(yggCore api.TopologyProvider) TopologyView {
	peers := yggCore.GetPeers()
	tree := yggCore.GetTree()
	out := TopologyView{
		Self: hex.EncodeToString(yggCore.PublicKey()),
	}
	for _, p := range peers {
		out.Peers = append(out.Peers, api.PeerInfo{
			URI:       p.URI,
			Up:        p.Up,
			Inbound:   p.Inbound,
			PublicKey: hex.EncodeToString(p.Key),
			Root:      hex.EncodeToString(p.Root),
			Port:      p.Port,
			Coords:    p.Coords,
		})
	}
	for _, t := range tree {
		out.Tree = append(out.Tree, api.TreeInfo{
			PublicKey: hex.EncodeToString(t.Key),
			Parent:    hex.EncodeToString(t.Parent),
			Sequence:  t.Sequence,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// DefaultBuildInfo populates a BuildInfo with the running Go version.
func DefaultBuildInfo() BuildInfo {
	return BuildInfo{GoVersion: runtime.Version()}
}
