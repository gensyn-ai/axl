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
	"example.com/internal/tcplisten"

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
	defaultBrideHost   = "127.0.0.1" // must exclude http prefix, must only be the host literal
	defaultRouterPort  = 9003
	defaultConfigPath  = "node-config.json"
	defaultListenUsage = "Custom listen address (optional)"
)

type ApiConfig struct {
	ApiPort    int    `json:"api_port"`
	RouterAddr string `json:"router_addr"`
	RouterPort int    `json:"router_port"`
	BridgeAddr string `json:"bridge_addr"`
	TCPPort    int    `json:"tcp_port"`
}

func defaultAPIConfig() ApiConfig {
	return ApiConfig{
		ApiPort:    defaultAPIPort,
		RouterAddr: defaultRouterHost,
		RouterPort: defaultRouterPort,
		BridgeAddr: defaultBrideHost,
		TCPPort:    defaultTCPPort,
	}
}

func applyOverrides(base *ApiConfig, ov ApiConfig) {
	if ov.ApiPort != 0 {
		base.ApiPort = ov.ApiPort
	}
	if ov.RouterAddr != "" {
		base.RouterAddr = ov.RouterAddr
	}
	if ov.RouterPort != 0 {
		base.RouterPort = ov.RouterPort
	}
	if ov.BridgeAddr != "" {
		base.BridgeAddr = ov.BridgeAddr
	}
	if ov.TCPPort != 0 {
		base.TCPPort = ov.TCPPort
	}
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("Node exited with error: \n%v", err)
	}
}

func run() error {
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
		return fmt.Errorf("open config %s: %w", *configPath, err)
	}
	defer file.Close()
	if _, err := cfg.ReadFrom(file); err != nil {
		return fmt.Errorf("parse config %s: %w", *configPath, err)
	}
	logger.Infof("Loaded Yggdrasil config from %s", *configPath)
	cfg.IfName = "none" // Required for userspace mode

	// Create API configuration overrides
	configBytes, err := os.ReadFile(*configPath)
	if err != nil {
		return fmt.Errorf("read config %s: %w", *configPath, err)
	}
	var overrides ApiConfig
	if err := json.Unmarshal(configBytes, &overrides); err == nil {
		applyOverrides(&apiCfg, overrides)
	}

	routerHost := strings.TrimRight(apiCfg.RouterAddr, "/")
	if routerHost == "" {
		routerHost = defaultRouterHost
	}
	routerURL = fmt.Sprintf("%s:%d/route", routerHost, apiCfg.RouterPort)
	logger.Infof("MCP Router URL: %s", routerURL)

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

	logger.Infof("Yggdrasil Userspace Node Started!")
	logger.Infof("Our IPv6: %s", yggCore.Address().String())
	logger.Infof("Our Public Key: %s", hex.EncodeToString(yggCore.PublicKey()))

	// Setup Userspace Network Stack (gVisor)
	tcpPort := apiCfg.TCPPort
	tcplisten.SetupNetworkStack(yggCore, tcpPort, routerURL)

	// Create HTTP Bridge
	handler := api.NewHandler(yggCore, tcpPort, tcplisten.NetStack)
	listenAddrStr := fmt.Sprintf("%s:%d", apiCfg.BridgeAddr, apiCfg.ApiPort)
	fmt.Println("Listening on", listenAddrStr)
	if err := http.ListenAndServe(listenAddrStr, handler); err != nil {
		return fmt.Errorf("HTTP server failed: %w", err)
	}
	return nil
}
