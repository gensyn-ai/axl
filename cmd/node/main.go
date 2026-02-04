package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"net/http"
	"os"
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

const TCPPort = 7000

func main() {
	// Command-line flags
	listenAddr := flag.String("listen", "", "Listen address for incoming peers (e.g., tls://0.0.0.0:9001). If set, runs as a server.")
	peerAddr := flag.String("peer", "", "Peer address to connect to (e.g., tls://1.2.3.4:9001). If not set and not listening, uses default public peer.")
	routerAddr := flag.String("router", "http://127.0.0.1:9003", "MCP router URL for forwarding tool requests")
	flag.Parse()

	// Set router URL
	routerURL = *routerAddr + "/route"
	fmt.Printf("MCP Router: %s\n", routerURL)

	// 1. Generate config
	cfg := config.GenerateConfig()
	cfg.IfName = "none"

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

	// 4. Start HTTP bridge for Application Layer
	http.HandleFunc("/topology", api.HandleTopology(yggCore))
	http.HandleFunc("/send", api.HandleSend(TCPPort, tcp.NetStack))
	http.HandleFunc("/recv", api.HandleRecv)
	http.HandleFunc("/mcp/", api.HandleMCP(TCPPort, tcp.NetStack))

	fmt.Println("Starting Local HTTP Bridge on localhost:9002...")
	fmt.Println("MCP HTTP transport available at /mcp/{service}/{peer_key}")
	if err := http.ListenAndServe("127.0.0.1:9002", nil); err != nil {
		log.Fatalf("HTTP Server failed: %v", err)
	}
}
