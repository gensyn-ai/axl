package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/gensyn-ai/axl/api"
	"github.com/gensyn-ai/axl/dashboard"
	"github.com/gensyn-ai/axl/internal/metrics"
	"github.com/gensyn-ai/axl/internal/tcp/listen"

	"github.com/gologme/log"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

// Build info, settable via -ldflags "-X main.buildVersion=... -X main.buildCommit=... -X main.buildTime=...".
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildTime    = "unknown"
)

var (
	yggCore *core.Core
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("Node exited with error: \n%v", err)
	}
}

func run() error {
	listenAddr := flag.String("listen", "", "Listen address override (optional)")
	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	dashboardEnabled := flag.Bool("dashboard", true, "Enable the in-process dashboard at /dashboard/")
	flag.Parse()

	// Load API configuration
	apiCfg, err := LoadAPIConfig(*configPath)
	if err != nil {
		return err
	}

	// Initialize metrics registry and log ring before the logger so log lines
	// flow into the dashboard's tail.
	reg := metrics.NewRegistry()
	metrics.Default = reg
	registerSeries(reg)
	reg.Start()
	defer reg.Stop()

	// Create logger that writes to both stdout and the dashboard log ring.
	var logSink io.Writer = os.Stdout
	if *dashboardEnabled {
		logSink = io.MultiWriter(os.Stdout, reg.Logs())
	}
	logger := log.New(logSink, "[node] ", 0)
	logger.EnableLevel("info")
	logger.EnableLevel("warn")
	logger.EnableLevel("error")

	// Create Yggdrasil configuration
	cfg := config.GenerateConfig()
	file, err := os.Open(*configPath)
	if err != nil {
		return fmt.Errorf("open config %s: %w", *configPath, err)
	}
	defer file.Close()
	if _, err := cfg.ReadFrom(file); err != nil {
		return fmt.Errorf("parse config %s: %w", *configPath, err)
	}
	logger.Infof("Loaded node config from %s", *configPath)
	cfg.IfName = "none" // Required for userspace mode

	// Apply security limits
	limits := apiCfg.ToSecurityLimits()
	api.MaxMessageSize = limits.MaxMessageSize
	listen.MaxConcurrentConns = limits.MaxConcConns
	listen.ConnReadTimeout = limits.ConnReadTimeout
	listen.ConnIdleTimeout = limits.ConnIdleTimeout
	logger.Infof("Max message size: %d bytes", api.MaxMessageSize)
	logger.Infof("Max concurrent connections: %d", listen.MaxConcurrentConns)
	logger.Infof("Connection read timeout: %s", listen.ConnReadTimeout)
	logger.Infof("Connection idle timeout: %s", listen.ConnIdleTimeout)

	// Start the Yggdrasil core
	options := []core.SetupOption{}
	listens := append([]string{}, cfg.Listen...)
	if *listenAddr != "" {
		logger.Infof("Overriding listen address: %s", *listenAddr)
		listens = append([]string{*listenAddr}, listens...)
	}
	for _, addr := range listens {
		options = append(options, core.ListenAddress(addr))
	}
	for _, peer := range cfg.Peers {
		logger.Infof("Configured peer: %s", peer)
		options = append(options, core.Peer{URI: peer})
	}

	yggCore, err := core.New(cfg.Certificate, logger, options...)
	if err != nil {
		return fmt.Errorf("start core: %w", err)
	}
	defer yggCore.Stop()

	logger.Infof("Gensyn Node Started!")
	logger.Infof("Our IPv6: %s", yggCore.Address().String())
	logger.Infof("Our Public Key: %s", hex.EncodeToString(yggCore.PublicKey()))

	// Setup Userspace Network Stack (gVisor)
	tcpPort := apiCfg.TCPPort

	mcpRouterHost := strings.TrimRight(apiCfg.McpRouterAddr, "/")
	mcpRouterUrl := ""
	if mcpRouterHost != "" {
		mcpRouterUrl = fmt.Sprintf("%s:%d/route", mcpRouterHost, apiCfg.McpRouterPort)
		logger.Infof("MCP Router URL: %s", mcpRouterUrl)
	}

	a2aUrl := ""
	if apiCfg.A2AAddr != "" {
		a2aUrl = fmt.Sprintf("%s:%d", apiCfg.A2AAddr, apiCfg.A2APort)
		logger.Infof("A2A Server URL: %s", a2aUrl)
	}
	listen.SetupNetworkStack(yggCore, tcpPort, mcpRouterUrl, a2aUrl)

	// Build HTTP mux with API + dashboard routes, wrapped in timing middleware.
	mux := http.NewServeMux()
	apiHandler := api.NewHandler(yggCore, tcpPort, listen.NetStack)
	mux.Handle("/topology", apiHandler)
	mux.Handle("/send", apiHandler)
	mux.Handle("/recv", apiHandler)
	mux.Handle("/mcp/", apiHandler)
	mux.Handle("/a2a/", apiHandler)

	if *dashboardEnabled {
		dashboard.Mount(mux, dashboard.Config{
			Reg:     reg,
			YggCore: yggCore,
			BuildInfo: dashboard.BuildInfo{
				Version:   buildVersion,
				Commit:    buildCommit,
				BuildTime: buildTime,
				GoVersion: runtime.Version(),
			},
			NodeConfig: dashboard.NodeConfigView{
				TCPPort:         apiCfg.TCPPort,
				APIPort:         apiCfg.ApiPort,
				BridgeAddr:      apiCfg.BridgeAddr,
				McpRouterAddr:   apiCfg.McpRouterAddr,
				A2AAddr:         apiCfg.A2AAddr,
				MaxMessageSize:  api.MaxMessageSize,
				MaxConcConns:    listen.MaxConcurrentConns,
				ConnReadTimeout: listen.ConnReadTimeout.String(),
				ConnIdleTimeout: listen.ConnIdleTimeout.String(),
			},
			StartTime: reg.StartTime(),
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/dashboard/", http.StatusFound)
				return
			}
			http.NotFound(w, r)
		})
		if apiCfg.BridgeAddr != "127.0.0.1" && apiCfg.BridgeAddr != "localhost" {
			logger.Warnf("Dashboard enabled on non-loopback address %s — exposed without auth", apiCfg.BridgeAddr)
		}
	}

	handler := api.TimingMiddleware(mux)
	listenAddrStr := fmt.Sprintf("%s:%d", apiCfg.BridgeAddr, apiCfg.ApiPort)
	fmt.Println("Listening on", listenAddrStr)
	if *dashboardEnabled {
		fmt.Printf("Dashboard at http://%s/dashboard/\n", listenAddrStr)
	}
	if err := http.ListenAndServe(listenAddrStr, handler); err != nil {
		return fmt.Errorf("HTTP server failed: %w", err)
	}
	return nil
}

// registerSeries wires up which counters/gauges become time-series.
// Called once at startup before reg.Start().
func registerSeries(reg *metrics.Registry) {
	s := reg.Series()
	// Rates from counters
	s.TrackRate("messages_in_per_sec", reg.Counter("messages_in_total"))
	s.TrackRate("messages_out_per_sec", reg.Counter("messages_out_total"))
	s.TrackRate("message_bytes_in_per_sec", reg.Counter("message_bytes_in_total"))
	s.TrackRate("message_bytes_out_per_sec", reg.Counter("message_bytes_out_total"))
	s.TrackRate("tcp_accepts_per_sec", reg.Counter("tcp_accepts_total"))
	s.TrackRate("http_requests_per_sec", reg.Counter("http_requests_total"))
	// Gauges sampled directly
	s.TrackGauge("tcp_active_conns", func() float64 { return float64(reg.Gauge("tcp_active_conns").Value()) })
	s.TrackGauge("recv_queue_depth", func() float64 { return float64(api.DefaultRecvQueue.Len()) })
	s.TrackGauge("runtime_goroutines", func() float64 { return float64(reg.Gauge("runtime_goroutines").Value()) })
	s.TrackGauge("runtime_heap_bytes", func() float64 { return float64(reg.Gauge("runtime_heap_bytes").Value()) })
	// Keep the gauge in sync with the live queue length (other code only updates on push/pop).
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			reg.Gauge("recv_queue_depth").Set(int64(api.DefaultRecvQueue.Len()))
		}
	}()
}
