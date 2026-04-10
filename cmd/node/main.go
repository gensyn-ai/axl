package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gensyn-ai/axl/api"
	"github.com/gensyn-ai/axl/internal/tcp/listen"

	"github.com/gologme/log"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
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
	flag.Parse()

	// Load API configuration
	apiCfg, err := LoadAPIConfig(*configPath)
	if err != nil {
		return err
	}

	// Create logger
	logger := log.New(os.Stdout, "[node] ", 0)
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

	// Create HTTP Bridge
	handler := api.NewHandler(yggCore, tcpPort, listen.NetStack)
	listenAddrStr := fmt.Sprintf("%s:%d", apiCfg.BridgeAddr, apiCfg.ApiPort)
	fmt.Println("Listening on", listenAddrStr)
	if err := http.ListenAndServe(listenAddrStr, handler); err != nil {
		return fmt.Errorf("HTTP server failed: %w", err)
	}
	return nil
}
