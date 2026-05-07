package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const (
	defaultAPIPort        = 9002
	defaultMcpRouterPort  = 9003
	defaultA2APort        = 9004
	defaultMcpRouterHost  = ""          // if hosting locally use http://127.0.0.1
	defaultBridgeHost     = "127.0.0.1" // must exclude http prefix, must only be the host literal for ListenAndServe
	defaultA2AHost        = ""          // if hosting locally use http://127.0.0.1
	defaultConfigPath     = "node-config.json"
	defaultMaxMessageSize = 16 * 1024 * 1024 // 16 MB
	defaultMaxConcConns   = 128
	defaultConnReadTimeout  = 60  // seconds
	defaultConnIdleTimeout  = 300 // seconds
)

// ApiConfig holds the HTTP API and TCP configuration for the node.
type ApiConfig struct {
	ApiPort         int    `json:"api_port"`
	McpRouterPort   int    `json:"router_port"`
	A2APort         int    `json:"a2a_port"`
	McpRouterAddr   string `json:"router_addr"`
	BridgeAddr      string `json:"bridge_addr"`
	A2AAddr         string `json:"a2a_addr"`
	MaxMessageSize  int    `json:"max_message_size"`
	MaxConcConns    int    `json:"max_concurrent_conns"`
	ConnReadTimeout int    `json:"conn_read_timeout_secs"`
	ConnIdleTimeout int    `json:"conn_idle_timeout_secs"`
}

// DefaultAPIConfig returns a new ApiConfig with default values.
func DefaultAPIConfig() ApiConfig {
	return ApiConfig{
		ApiPort:         defaultAPIPort,
		McpRouterPort:   defaultMcpRouterPort,
		A2APort:         defaultA2APort,
		McpRouterAddr:   defaultMcpRouterHost,
		BridgeAddr:      defaultBridgeHost,
		A2AAddr:         defaultA2AHost,
		MaxMessageSize:  defaultMaxMessageSize,
		MaxConcConns:    defaultMaxConcConns,
		ConnReadTimeout: defaultConnReadTimeout,
		ConnIdleTimeout: defaultConnIdleTimeout,
	}
}

// LoadAPIConfig loads the API configuration from the specified config file.
// It starts with defaults and applies overrides from the JSON config. The
// returned warnings slice surfaces non-fatal config issues (e.g. deprecated
// fields) for the caller to log.
func LoadAPIConfig(configPath string) (ApiConfig, []string, error) {
	cfg := DefaultAPIConfig()

	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		return cfg, nil, fmt.Errorf("read config %s: %w", configPath, err)
	}

	var overrides ApiConfig
	if err := json.Unmarshal(configBytes, &overrides); err != nil {
		// JSON unmarshal errors are non-fatal - we just use defaults
		return cfg, nil, nil
	}

	applyOverrides(&cfg, overrides)
	return cfg, deprecationWarnings(configBytes), nil
}

// deprecationWarnings reports any deprecated config keys present in the raw
// JSON. Used to give users a heads-up when they upgrade past a
// removed setting that would otherwise silently be ignored.
func deprecationWarnings(configBytes []byte) []string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(configBytes, &raw); err != nil {
		return nil
	}
	var warnings []string
	if _, ok := raw["tcp_port"]; ok {
		warnings = append(warnings, "config field 'tcp_port' is deprecated and ignored; the overlay TCP port is fixed at 7000 network-wide")
	}
	return warnings
}

func applyOverrides(base *ApiConfig, ov ApiConfig) {
	if ov.ApiPort != 0 {
		base.ApiPort = ov.ApiPort
	}
	if ov.McpRouterPort != 0 {
		base.McpRouterPort = ov.McpRouterPort
	}
	if ov.A2APort != 0 {
		base.A2APort = ov.A2APort
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

// SecurityLimits holds the applied security configuration for easy access.
type SecurityLimits struct {
	MaxMessageSize   uint32
	MaxConcConns     int
	ConnReadTimeout  time.Duration
	ConnIdleTimeout  time.Duration
}

// ToSecurityLimits converts ApiConfig values to the appropriate types for use
// throughout the application.
func (cfg *ApiConfig) ToSecurityLimits() SecurityLimits {
	return SecurityLimits{
		MaxMessageSize:  uint32(cfg.MaxMessageSize),
		MaxConcConns:    cfg.MaxConcConns,
		ConnReadTimeout: time.Duration(cfg.ConnReadTimeout) * time.Second,
		ConnIdleTimeout: time.Duration(cfg.ConnIdleTimeout) * time.Second,
	}
}
