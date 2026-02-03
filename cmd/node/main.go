package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
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

	// MCP session tracking for Streamable HTTP transport
	mcpSessions     = map[string]bool{}
	mcpSessionMutex sync.RWMutex
)

const TCPPort = 7000

func main() {
	// Command-line flags
	listenAddr := flag.String("listen", "", "Listen address for incoming peers (e.g., tls://0.0.0.0:9001). If set, runs as a server.")
	peerAddr := flag.String("peer", "", "Peer address to connect to (e.g., tls://1.2.3.4:9001). If not set and not listening, uses default public peer.")
	routerAddr := flag.String("router", "http://127.0.0.1:9003", "MCP router URL for forwarding tool requests")
	privateKeyHex := flag.String("private-key", "", "Hex-encoded private key for consistent peer ID (optional)")
	flag.Parse()

	// Set router URL
	routerURL = *routerAddr + "/route"
	fmt.Printf("MCP Router: %s\n", routerURL)

	// 1. Generate config
	cfg := config.GenerateConfig()
	cfg.IfName = "none"

	// Use supplied private key if provided for consistent peer ID
	if *privateKeyHex != "" {
		privateKeyBytes, err := hex.DecodeString(*privateKeyHex)
		if err != nil {
			log.Fatalf("Failed to decode private key: %v", err)
		}
		if len(privateKeyBytes) != 32 {
			log.Fatalf("Invalid private key length: expected 32 bytes, got %d", len(privateKeyBytes))
		}
		cfg.PrivateKey = config.KeyBytes(privateKeyBytes)
		fmt.Printf("Using supplied private key for consistent peer ID\n")
	} else {
		fmt.Printf("Using generated private key (peer ID will change each restart)\n")
	}

	// Create logger
	logger := log.New(os.Stdout, "[ygg] ", 0)
	logger.EnableLevel("info")
	logger.EnableLevel("warn")
	logger.EnableLevel("error")

	// 2. Start the Yggdrasil core
	var err error
	options := []core.SetupOption{}

	if *listenAddr != "" {
		fmt.Printf("Will listen on: %s\n", *listenAddr)
		options = append(options, core.ListenAddress(*listenAddr))
	}

	if *peerAddr != "" {
		fmt.Printf("Will connect to peer: %s\n", *peerAddr)
		options = append(options, core.Peer{URI: *peerAddr})
	} else if *listenAddr == "" {
		defaultPeer := "tls://34.173.99.229:9001"
		fmt.Printf("Using default peer: %s\n", defaultPeer)
		options = append(options, core.Peer{URI: defaultPeer})
	}

	yggCore, err = core.New(cfg.Certificate, logger, options...)
	if err != nil {
		log.Fatalf("Failed to start Yggdrasil core: %v", err)
	}
	defer yggCore.Stop()

	fmt.Println("Yggdrasil Userspace Node Started!")
	fmt.Printf("Our IPv6: %s\n", yggCore.Address().String())
	fmt.Printf("Our Public Key: %s\n", hex.EncodeToString(yggCore.PublicKey()))

	// 3. Setup Userspace Network Stack (gVisor)
	tcp.SetupNetworkStack(yggCore, TCPPort)

	// 4. Start HTTP bridge for Python
	http.HandleFunc("/topology", api.HandleTopology(yggCore))
	http.HandleFunc("/send", api.HandleSend(TCPPort, tcp.NetStack))
	http.HandleFunc("/recv", api.HandleRecv)
	// http.HandleFunc("/mcp/", api.HandleMCP)

	fmt.Println("Starting Local HTTP Bridge on localhost:9002...")
	fmt.Println("MCP HTTP transport available at /mcp/{service}/{peer_key}")
	if err := http.ListenAndServe("127.0.0.1:9002", nil); err != nil {
		log.Fatalf("HTTP Server failed: %v", err)
	}
}

// // RouterRequest is sent to the MCP router
// type RouterRequest struct {
// 	Service string          `json:"service"`
// 	Request json.RawMessage `json:"request"`
// 	FromKey string          `json:"from_key"`
// }

// // RouterResponse is returned by the MCP router
// type RouterResponse struct {
// 	Response json.RawMessage `json:"response"`
// 	Error    string          `json:"error"`
// }

// // forwardToRouter forwards an MCP request to the router service
// func forwardToRouter(service string, request json.RawMessage, fromKey string) (json.RawMessage, error) {
// 	// Build router request
// 	routerReq := RouterRequest{
// 		Service: service,
// 		Request: request,
// 		FromKey: fromKey,
// 	}

// 	reqBody, err := json.Marshal(routerReq)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to marshal request: %w", err)
// 	}

// 	// Send to router
// 	resp, err := httpClient.Post(routerURL, "application/json", bytes.NewReader(reqBody))
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to contact router: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	// Read response
// 	respBody, err := io.ReadAll(resp.Body)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to read router response: %w", err)
// 	}

// 	// Parse router response
// 	var routerResp RouterResponse
// 	if err := json.Unmarshal(respBody, &routerResp); err != nil {
// 		return nil, fmt.Errorf("failed to parse router response: %w", err)
// 	}

// 	// Check for router-level error
// 	if routerResp.Error != "" {
// 		return nil, fmt.Errorf("router error: %s", routerResp.Error)
// 	}

// 	return routerResp.Response, nil
// }

// // sendResponse sends a response back to a peer
// func sendResponse(conn net.Conn, data []byte) error {
// 	lenBuf := make([]byte, 4)
// 	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))

// 	if _, err := conn.Write(lenBuf); err != nil {
// 		return fmt.Errorf("failed to write length: %w", err)
// 	}
// 	if _, err := conn.Write(data); err != nil {
// 		return fmt.Errorf("failed to write data: %w", err)
// 	}
// 	return nil
// }
