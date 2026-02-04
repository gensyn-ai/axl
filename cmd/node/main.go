package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"example.com/api"
	"example.com/internal/tcp"

	"github.com/gologme/log"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

var (
	yggCore *core.Core

	// MCP Router URL - all MCP requests are forwarded here (set via flag)
	routerURL  string
	httpClient = &http.Client{Timeout: 30 * time.Second}
)

const (
	defaultTCPPort     = 7000
	defaultAPIPort     = 9002
	defaultRouterHost  = "http://127.0.0.1"
	defaultRouterPort  = 9003
	defaultConfigPath  = "node-config.json"
	defaultListenUsage = "Custom listen address (optional)"
)

type ApiConfig struct {
	ApiPort    int    `json:"api_port"`
	RouterAddr string `json:"router_addr"`
	RouterPort int    `json:"router_port"`
	TCPPort    int    `json:"tcp_port"`
}

func defaultAPIConfig() ApiConfig {
	return ApiConfig{
		ApiPort:    defaultAPIPort,
		RouterAddr: defaultRouterHost,
		RouterPort: defaultRouterPort,
		TCPPort:    defaultTCPPort,
	}
}

type apiConfigOverrides struct {
	ApiPort    *int    `json:"api_port"`
	RouterAddr *string `json:"router_addr"`
	RouterPort *int    `json:"router_port"`
	TCPPort    *int    `json:"tcp_port"`
}

func applyOverrides(base *ApiConfig, ov apiConfigOverrides) {
	if ov.ApiPort != nil {
		base.ApiPort = *ov.ApiPort
	}
	if ov.RouterAddr != nil {
		base.RouterAddr = *ov.RouterAddr
	}
	if ov.RouterPort != nil {
		base.RouterPort = *ov.RouterPort
	}
	if ov.TCPPort != nil {
		base.TCPPort = *ov.TCPPort
	}
}

func main() {
	apiCfg := defaultAPIConfig()
	listenAddr := flag.String("listen", "", "Listen address override (optional)")
	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	flag.Parse()

	// Create logger
	logger := log.New(os.Stdout, "[ygg] ", 0)
	logger.EnableLevel("info")
	logger.EnableLevel("warn")
	logger.EnableLevel("error")

	// Create Yggdrasil configuration
	cfg := config.GenerateConfig()
	file, err := os.Open(*configPath)
	if err != nil {
		logger.Fatalf("Failed to open config file %s: %v", *configPath, err)
	}
	defer file.Close()
	if _, err := cfg.ReadFrom(file); err != nil {
		logger.Fatalf("Failed to parse Yggdrasil config %s: %v", *configPath, err)
	}
	logger.Infof("Loaded Yggdrasil config from %s", *configPath)
	cfg.IfName = "none" // Required for userspace mode

	// Create API configuration overrides
	configBytes, err := os.ReadFile(*configPath)
	if err != nil {
		logger.Fatalf("Failed to read config file %s: %v", *configPath, err)
	}
	var overrides apiConfigOverrides
	if err := json.Unmarshal(configBytes, &overrides); err != nil {
		logger.Warnf("Failed to parse API overrides: %v", err)
	} else {
		applyOverrides(&apiCfg, overrides)
	}

	routerHost := strings.TrimRight(apiCfg.RouterAddr, "/")
	if routerHost == "" {
		routerHost = defaultRouterHost
	}
	routerPort := apiCfg.RouterPort
	if routerPort == 0 {
		routerPort = defaultRouterPort
	}
	routerURL = fmt.Sprintf("%s:%d/route", routerHost, routerPort)
	logger.Infof("MCP Router URL: %s", routerURL)

	// Start the Yggdrasil core
	options := []core.SetupOption{}
	listens := append([]string{}, cfg.Listen...)
	if *listenAddr != "" {
		logger.Infof("Overriding listen address: %s", *listenAddr)
		listens = append([]string{*listenAddr}, listens...)
	}
	if len(listens) == 0 {
		logger.Warnf("No listen addresses configured; node will operate outbound-only")
	}
	for _, addr := range listens {
		options = append(options, core.ListenAddress(addr))
	}
	if len(cfg.Peers) == 0 {
		logger.Warnf("No peers configured in %s; node will rely on listeners only", *configPath)
	}
	for _, peer := range cfg.Peers {
		logger.Infof("Configured peer: %s", peer)
		options = append(options, core.Peer{URI: peer})
	}

	tcpPort := apiCfg.TCPPort
	if tcpPort == 0 {
		tcpPort = defaultTCPPort
	}

	yggCore, err := core.New(cfg.Certificate, logger, options...)
	if err != nil {
		logger.Fatalf("Failed to start Yggdrasil core: %v", err)
	}
	defer yggCore.Stop()

	logger.Infof("Yggdrasil Userspace Node Started!")
	logger.Infof("Our IPv6: %s", yggCore.Address().String())
	logger.Infof("Our Public Key: %s", hex.EncodeToString(yggCore.PublicKey()))

	// Setup Userspace Network Stack (gVisor)
	tcp.SetupNetworkStack(yggCore, tcpPort)

	// Start HTTP bridge for Application Layer
	http.HandleFunc("/topology", api.HandleTopology(yggCore))
	http.HandleFunc("/send", api.HandleSend(tcpPort, tcp.NetStack))
	http.HandleFunc("/recv", api.HandleRecv)
	http.HandleFunc("/mcp/", api.HandleMCP(tcpPort, tcp.NetStack))

	apiPort := apiCfg.ApiPort
	if apiPort == 0 {
		apiPort = defaultAPIPort
	}
	listenAddrStr := fmt.Sprintf("127.0.0.1:%d", apiPort)
	if err := http.ListenAndServe(listenAddrStr, nil); err != nil {
		logger.Fatalf("HTTP Server failed: %v", err)
	}
}
