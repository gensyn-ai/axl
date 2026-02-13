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
	"example.com/internal/tcp/listen"

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
	defaultTCPPort        = 7000
	defaultAPIPort        = 9002
	defaultMcpRouterPort  = 9003
	defaultA2APort        = 9004
	defaultMcpRouterHost  = ""          // if hosting locally use http://127.0.0.1
	defaultBrideHost      = "127.0.0.1" // must exclude http prefix, must only be the host literal for ListenAndServe
	defaultA2AHost        = ""          // if hosting locally use http://127.0.0.1
	defaultConfigPath      = "node-config.json"
	defaultMaxMessageSize  = 16 * 1024 * 1024 // 16 MB
	defaultMaxConcConns    = 128
	defaultConnReadTimeout = 60  // seconds
	defaultConnIdleTimeout = 300 // seconds
)

type ApiConfig struct {
	TCPPort        int    `json:"tcp_port"`
	ApiPort        int    `json:"api_port"`
	McpRouterPort  int    `json:"router_port"`
	A2APort        int    `json:"a2a_port"`
	McpRouterAddr  string `json:"router_addr"`
	BridgeAddr     string `json:"bridge_addr"`
	A2AAddr        string `json:"a2a_addr"`
	MaxMessageSize int `json:"max_message_size"`
	MaxConcConns   int `json:"max_concurrent_conns"`
	ConnReadTimeout int `json:"conn_read_timeout_secs"`
	ConnIdleTimeout int `json:"conn_idle_timeout_secs"`
}

func defaultAPIConfig() ApiConfig {
	return ApiConfig{
		TCPPort:        defaultTCPPort,
		ApiPort:        defaultAPIPort,
		McpRouterPort:  defaultMcpRouterPort,
		A2APort:        defaultA2APort,
		McpRouterAddr:  defaultMcpRouterHost,
		BridgeAddr:     defaultBrideHost,
		A2AAddr:        defaultA2AHost,
		MaxMessageSize:  defaultMaxMessageSize,
		MaxConcConns:    defaultMaxConcConns,
		ConnReadTimeout: defaultConnReadTimeout,
		ConnIdleTimeout: defaultConnIdleTimeout,
	}
}

func applyOverrides(base *ApiConfig, ov ApiConfig) {
	if ov.TCPPort != 0 {
		base.TCPPort = ov.TCPPort
	}
	if ov.ApiPort != 0 {
		base.ApiPort = ov.ApiPort
	}
	if ov.McpRouterPort != 0 {
		base.McpRouterPort = ov.McpRouterPort
	}
	if ov.McpRouterAddr != "" {
		base.McpRouterAddr = ov.McpRouterAddr
	}
	if ov.BridgeAddr != "" {
		base.BridgeAddr = ov.BridgeAddr
	}
	if ov.A2AAddr != "" {
		base.A2AAddr = ov.A2AAddr
	}
	if ov.MaxMessageSize != 0 {
		base.MaxMessageSize = ov.MaxMessageSize
	}
	if ov.MaxConcConns != 0 {
		base.MaxConcConns = ov.MaxConcConns
	}
	if ov.ConnReadTimeout != 0 {
		base.ConnReadTimeout = ov.ConnReadTimeout
	}
	if ov.ConnIdleTimeout != 0 {
		base.ConnIdleTimeout = ov.ConnIdleTimeout
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

	// Apply security limits
	api.MaxMessageSize = uint32(apiCfg.MaxMessageSize)
	listen.MaxConcurrentConns = apiCfg.MaxConcConns
	listen.ConnReadTimeout = time.Duration(apiCfg.ConnReadTimeout) * time.Second
	listen.ConnIdleTimeout = time.Duration(apiCfg.ConnIdleTimeout) * time.Second
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

	logger.Infof("Yggdrasil Userspace Node Started!")
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

	// Create HTTP Bridge
	handler := api.NewHandler(yggCore, tcpPort, listen.NetStack)
	listenAddrStr := fmt.Sprintf("%s:%d", apiCfg.BridgeAddr, apiCfg.ApiPort)
	fmt.Println("Listening on", listenAddrStr)
	if err := http.ListenAndServe(listenAddrStr, handler); err != nil {
		return fmt.Errorf("HTTP server failed: %w", err)
	}
	return nil
}
