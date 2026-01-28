package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gologme/log"
	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

var (
	yggCore   *core.Core
	netStack  *stack.Stack
	recvMutex sync.Mutex
	recvQueue []ReceivedMessage

	// MCP Router URL - all MCP requests are forwarded here (set via flag)
	routerURL  string
	httpClient = &http.Client{Timeout: 30 * time.Second}

	// MCP session tracking for Streamable HTTP transport
	mcpSessions     = map[string]bool{}
	mcpSessionMutex sync.RWMutex
)

// ReceivedMessage holds incoming data with sender info
type ReceivedMessage struct {
	FromKey string `json:"from_key"`
	Data    []byte `json:"data"`
}

// SendRequest is what Python sends to /send
type SendRequest struct {
	DestinationKey string `json:"destination_key"` // Hex-encoded public key
	Data           []byte `json:"data"`
}

// TopologyInfo returned by /topology
type TopologyInfo struct {
	OurIPv6      string     `json:"our_ipv6"`
	OurPublicKey string     `json:"our_public_key"`
	Peers        []PeerInfo `json:"peers"`
	Tree         []TreeInfo `json:"tree"`
}

type PeerInfo struct {
	URI       string   `json:"uri"`
	Up        bool     `json:"up"`
	Inbound   bool     `json:"inbound"`
	PublicKey string   `json:"public_key"`
	Root      string   `json:"root"`
	Port      uint64   `json:"port"`
	Coords    []uint64 `json:"coords"`
}

type TreeInfo struct {
	PublicKey string `json:"public_key"`
	Parent    string `json:"parent"`
	Sequence  uint64 `json:"sequence"`
}

// MCPMessage wraps an MCP request with routing info
type MCPMessage struct {
	Service string          `json:"service"` // Target MCP service name (e.g., "weather")
	Request json.RawMessage `json:"request"` // The JSON-RPC request to forward
}

// MCPResponse wraps an MCP response
type MCPResponse struct {
	Service  string          `json:"service"`
	Response json.RawMessage `json:"response"`
	Error    string          `json:"error,omitempty"`
}

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
	setupNetworkStack()

	// 4. Start HTTP bridge for Python
	http.HandleFunc("/topology", handleTopology)
	http.HandleFunc("/send", handleSend)
	http.HandleFunc("/recv", handleRecv)
	http.HandleFunc("/mcp/", handleMCP)

	fmt.Println("Starting Local HTTP Bridge on localhost:9002...")
	fmt.Println("MCP HTTP transport available at /mcp/{service}/{peer_key}")
	if err := http.ListenAndServe("127.0.0.1:9002", nil); err != nil {
		log.Fatalf("HTTP Server failed: %v", err)
	}
}

func setupNetworkStack() {
	// Create ipv6rwc wrapper
	rwc := ipv6rwc.NewReadWriteCloser(yggCore)

	// Create channel endpoint
	// 1280 is min IPv6 MTU
	// Increased buffer size from 1024 to 8192 for better throughput
	ep := channel.New(8192, 1280, "")

	// Pump: Inbound (Ygg -> Stack)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := rwc.Read(buf)
			if err != nil {
				log.Printf("RWC Read error: %v", err)
				break
			}
			if n == 0 {
				continue
			}

			// Inject into stack
			// Create packet buffer with data
			view := buffer.NewViewWithData(append([]byte(nil), buf[:n]...))
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
				Payload: buffer.MakeWithView(view),
			})
			ep.InjectInbound(header.IPv6ProtocolNumber, pkt)
		}
	}()

	// Pump: Outbound (Stack -> Ygg)
	go func() {
		for {
			pkt := ep.Read()
			if pkt == nil {
				time.Sleep(1 * time.Millisecond) // Poll if empty
				continue
			}

			// Serialize packet to bytes
			// Use AsSlices() to get all data views
			// Pre-allocate buffer to avoid repeated allocations
			slices := pkt.AsSlices()
			totalLen := 0
			for _, v := range slices {
				totalLen += len(v)
			}
			bs := make([]byte, 0, totalLen)
			for _, v := range slices {
				bs = append(bs, v...)
			}

			rwc.Write(bs)
			pkt.DecRef()
		}
	}()

	// Initialize Stack with TCP performance tuning
	netStack = stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	// Configure TCP stack options for better performance
	// Increase send/receive buffer sizes (default is often too small)
	netStack.SetTransportProtocolOption(tcp.ProtocolNumber, &tcpip.TCPSendBufferSizeRangeOption{
		Min:     4096,
		Default: 1024 * 1024,     // 1 MB default
		Max:     8 * 1024 * 1024, // 8 MB max
	})
	netStack.SetTransportProtocolOption(tcp.ProtocolNumber, &tcpip.TCPReceiveBufferSizeRangeOption{
		Min:     4096,
		Default: 1024 * 1024,     // 1 MB default
		Max:     8 * 1024 * 1024, // 8 MB max
	})

	// Create NIC
	nicID := tcpip.NICID(1)
	if err := netStack.CreateNIC(nicID, ep); err != nil {
		log.Fatalf("CreateNIC failed: %v", err)
	}

	// Add Protocol Address
	// Yggdrasil Address
	yggAddr := tcpip.AddrFromSlice(yggCore.Address())
	protocolAddr := tcpip.ProtocolAddress{
		Protocol: header.IPv6ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   yggAddr,
			PrefixLen: 64,
		},
	}
	if err := netStack.AddProtocolAddress(nicID, protocolAddr, stack.AddressProperties{}); err != nil {
		log.Fatalf("AddProtocolAddress failed: %v", err)
	}

	// Add Route
	netStack.SetRouteTable([]tcpip.Route{
		{
			Destination: header.IPv6EmptySubnet,
			NIC:         nicID,
		},
	})

	// Start TCP Listener
	go startTCPListener()
}

func startTCPListener() {
	// Listen on [::]:7000
	listener, err := gonet.ListenTCP(netStack, tcpip.FullAddress{
		NIC:  0,
		Port: uint16(TCPPort),
	}, header.IPv6ProtocolNumber)

	if err != nil {
		log.Fatalf("ListenTCP failed: %v", err)
	}

	fmt.Printf("TCP Listener started on port %d\n", TCPPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go handleTCPConn(conn)
	}
}

// RouterRequest is sent to the MCP router
type RouterRequest struct {
	Service string          `json:"service"`
	Request json.RawMessage `json:"request"`
	FromKey string          `json:"from_key"`
}

// RouterResponse is returned by the MCP router
type RouterResponse struct {
	Response json.RawMessage `json:"response"`
	Error    string          `json:"error"`
}

// forwardToRouter forwards an MCP request to the router service
func forwardToRouter(service string, request json.RawMessage, fromKey string) (json.RawMessage, error) {
	// Build router request
	routerReq := RouterRequest{
		Service: service,
		Request: request,
		FromKey: fromKey,
	}

	reqBody, err := json.Marshal(routerReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send to router
	resp, err := httpClient.Post(routerURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to contact router: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read router response: %w", err)
	}

	// Parse router response
	var routerResp RouterResponse
	if err := json.Unmarshal(respBody, &routerResp); err != nil {
		return nil, fmt.Errorf("failed to parse router response: %w", err)
	}

	// Check for router-level error
	if routerResp.Error != "" {
		return nil, fmt.Errorf("router error: %s", routerResp.Error)
	}

	return routerResp.Response, nil
}

// sendResponse sends a response back to a peer
func sendResponse(conn net.Conn, data []byte) error {
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))

	if _, err := conn.Write(lenBuf); err != nil {
		return fmt.Errorf("failed to write length: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}
	return nil
}

func handleTCPConn(conn net.Conn) {
	defer conn.Close()

	// Identify Sender
	remoteAddrStr := conn.RemoteAddr().String()
	host, _, _ := net.SplitHostPort(remoteAddrStr)

	// Convert Host IPv6 -> PublicKey
	fromKey := ""
	ip := net.ParseIP(host)
	if ip != nil {
		var addrBytes [16]byte
		copy(addrBytes[:], ip.To16())
		yggAddr := address.Address(addrBytes)
		key := yggAddr.GetKey()
		fromKey = hex.EncodeToString(key)
	}

	log.Printf("Connection from peer %s...", fromKey[:16])

	// Protocol: Length(4 bytes) + Data
	for {
		// Read Length
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			if err != io.EOF {
				log.Printf("Read length error: %v", err)
			}
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)

		// Read Data
		dataBuf := make([]byte, length)
		if _, err := io.ReadFull(conn, dataBuf); err != nil {
			log.Printf("Read data error: %v", err)
			return
		}

		// Try to parse as MCP message
		var mcpMsg MCPMessage
		if err := json.Unmarshal(dataBuf, &mcpMsg); err == nil && mcpMsg.Service != "" {
			// This is an MCP request - forward to router
			log.Printf("Forwarding MCP request to router for service: %s", mcpMsg.Service)

			respData, err := forwardToRouter(mcpMsg.Service, mcpMsg.Request, fromKey)

			var mcpResp MCPResponse
			mcpResp.Service = mcpMsg.Service

			if err != nil {
				log.Printf("MCP forward error: %v", err)
				mcpResp.Error = err.Error()
			} else if respData != nil {
				mcpResp.Response = respData
			} else {
				// No response needed (notification)
				continue
			}

			// Send response back to peer
			respBytes, _ := json.Marshal(mcpResp)
			if err := sendResponse(conn, respBytes); err != nil {
				log.Printf("Failed to send response: %v", err)
			}
			continue
		}

		// Not an MCP message - queue it for /recv (legacy behavior)
		msg := ReceivedMessage{
			FromKey: fromKey,
			Data:    dataBuf,
		}

		recvMutex.Lock()
		if len(recvQueue) >= 100 {
			recvQueue = recvQueue[1:]
		}
		recvQueue = append(recvQueue, msg)
		recvMutex.Unlock()
	}
}

func handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get destination key from header (raw binary, no JSON/base64)
	destKeyHex := r.Header.Get("X-Destination-Key")
	if destKeyHex == "" {
		http.Error(w, "Missing X-Destination-Key header", http.StatusBadRequest)
		return
	}

	// Decode destination public key
	destKeyBytes, err := hex.DecodeString(destKeyHex)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid destination key: %v", err), http.StatusBadRequest)
		return
	}

	// Convert Key -> IPv6 Address
	if len(destKeyBytes) != 32 {
		http.Error(w, "Invalid key length", http.StatusBadRequest)
		return
	}
	var keyArr [32]byte
	copy(keyArr[:], destKeyBytes)
	destAddr := address.AddrForKey(keyArr[:])

	// Read raw binary body directly (no JSON/base64 decoding)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusBadRequest)
		return
	}

	// Dial via gVisor
	destIP := tcpip.AddrFromSlice(destAddr[:])

	conn, err := gonet.DialTCP(netStack, tcpip.FullAddress{
		NIC:  0,
		Addr: destIP,
		Port: uint16(TCPPort),
	}, header.IPv6ProtocolNumber)

	if err != nil {
		http.Error(w, fmt.Sprintf("DialTCP failed: %v", err), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Write Length Prefix
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))

	if _, err := conn.Write(lenBuf); err != nil {
		http.Error(w, fmt.Sprintf("Write length failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Write Data
	if _, err := conn.Write(data); err != nil {
		http.Error(w, fmt.Sprintf("Write data failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Try to read a response from the peer (for MCP request/response pattern)
	// Set a read deadline so we don't block forever on non-MCP sends
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Read 4-byte length prefix
	respLenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, respLenBuf); err != nil {
		// No response (fire-and-forget message), return just the sent bytes
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Sent-Bytes", fmt.Sprintf("%d", len(data)))
		w.WriteHeader(http.StatusOK)
		return
	}

	respLen := binary.BigEndian.Uint32(respLenBuf)
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		http.Error(w, fmt.Sprintf("Failed to read response data: %v", err), http.StatusBadGateway)
		return
	}

	// Return the peer's response
	w.Header().Set("Content-Type", "application/json")
	w.Write(respBuf)
}

func handleRecv(w http.ResponseWriter, r *http.Request) {
	recvMutex.Lock()
	defer recvMutex.Unlock()

	if len(recvQueue) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Pop first message
	msg := recvQueue[0]
	recvQueue = recvQueue[1:]

	// Return raw binary with sender key in header (no JSON/base64)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-From-Key", msg.FromKey)
	w.Write(msg.Data)
}

// handleMCP implements the MCP Streamable HTTP transport.
// URL format: /mcp/{service}/{peer_key}
// Claude Code connects here as a remote MCP server via HTTP transport.
func handleMCP(w http.ResponseWriter, r *http.Request) {
	// Parse path: /mcp/{service}/{peer_key}
	path := strings.TrimPrefix(r.URL.Path, "/mcp/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "URL must be /mcp/{service}/{peer_key}", http.StatusBadRequest)
		return
	}
	service := parts[0]
	peerKeyHex := parts[1]

	switch r.Method {
	case "POST":
		handleMCPPost(w, r, service, peerKeyHex)
	case "DELETE":
		// Session termination
		sessionID := r.Header.Get("Mcp-Session-Id")
		if sessionID != "" {
			mcpSessionMutex.Lock()
			delete(mcpSessions, sessionID)
			mcpSessionMutex.Unlock()
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleMCPPost(w http.ResponseWriter, r *http.Request, service string, peerKeyHex string) {
	// Read the JSON-RPC request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusBadRequest)
		return
	}

	// Parse to check the method (we need to handle initialize locally for session management)
	var jsonrpcReq struct {
		Method string `json:"method"`
		ID     any    `json:"id"`
	}
	if err := json.Unmarshal(body, &jsonrpcReq); err != nil {
		http.Error(w, "Invalid JSON-RPC", http.StatusBadRequest)
		return
	}

	// Handle notifications/initialized locally (no response needed)
	if jsonrpcReq.Method == "notifications/initialized" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Handle initialize locally - create session and forward to peer
	sessionID := r.Header.Get("Mcp-Session-Id")
	if jsonrpcReq.Method == "initialize" {
		// Generate session ID
		sessionID = fmt.Sprintf("mcp-%s-%s-%d", service, peerKeyHex[:8], time.Now().UnixNano())
		mcpSessionMutex.Lock()
		mcpSessions[sessionID] = true
		mcpSessionMutex.Unlock()
	} else if sessionID != "" {
		// Validate existing session
		mcpSessionMutex.RLock()
		valid := mcpSessions[sessionID]
		mcpSessionMutex.RUnlock()
		if !valid {
			http.Error(w, "Invalid or expired session", http.StatusNotFound)
			return
		}
	}

	// Wrap the JSON-RPC request in our MCP envelope and send to peer
	envelope := MCPMessage{
		Service: service,
		Request: body,
	}
	envelopeBytes, _ := json.Marshal(envelope)

	// Decode peer key
	destKeyBytes, err := hex.DecodeString(peerKeyHex)
	if err != nil || len(destKeyBytes) != 32 {
		http.Error(w, "Invalid peer key", http.StatusBadRequest)
		return
	}
	var keyArr [32]byte
	copy(keyArr[:], destKeyBytes)
	destAddr := address.AddrForKey(keyArr[:])

	// Dial the remote peer
	destIP := tcpip.AddrFromSlice(destAddr[:])
	conn, err := gonet.DialTCP(netStack, tcpip.FullAddress{
		NIC:  0,
		Addr: destIP,
		Port: uint16(TCPPort),
	}, header.IPv6ProtocolNumber)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to reach peer: %v", err), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	// Send length-prefixed envelope
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(envelopeBytes)))
	if _, err := conn.Write(lenBuf); err != nil {
		http.Error(w, "Failed to send to peer", http.StatusBadGateway)
		return
	}
	if _, err := conn.Write(envelopeBytes); err != nil {
		http.Error(w, "Failed to send to peer", http.StatusBadGateway)
		return
	}

	// Read the response from the peer
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	respLenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, respLenBuf); err != nil {
		http.Error(w, "No response from peer", http.StatusGatewayTimeout)
		return
	}
	respLen := binary.BigEndian.Uint32(respLenBuf)
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		http.Error(w, "Failed to read peer response", http.StatusBadGateway)
		return
	}

	// Parse the MCPResponse envelope to extract the inner JSON-RPC response
	var mcpResp MCPResponse
	if err := json.Unmarshal(respBuf, &mcpResp); err != nil {
		http.Error(w, "Invalid response from peer", http.StatusBadGateway)
		return
	}

	if mcpResp.Error != "" {
		// Return a JSON-RPC error
		errResp := map[string]any{
			"jsonrpc": "2.0",
			"id":      jsonrpcReq.ID,
			"error":   map[string]any{"code": -32603, "message": mcpResp.Error},
		}
		w.Header().Set("Content-Type", "application/json")
		if sessionID != "" {
			w.Header().Set("Mcp-Session-Id", sessionID)
		}
		json.NewEncoder(w).Encode(errResp)
		return
	}

	// Return the inner JSON-RPC response directly
	w.Header().Set("Content-Type", "application/json")
	if sessionID != "" {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}
	w.Write(mcpResp.Response)
}

func handleTopology(w http.ResponseWriter, r *http.Request) {
	peers := yggCore.GetPeers()
	tree := yggCore.GetTree()

	var peerInfos []PeerInfo
	for _, p := range peers {
		peerInfos = append(peerInfos, PeerInfo{
			URI:       p.URI,
			Up:        p.Up,
			Inbound:   p.Inbound,
			PublicKey: hex.EncodeToString(p.Key),
			Root:      hex.EncodeToString(p.Root),
			Port:      p.Port,
			Coords:    p.Coords,
		})
	}

	var treeInfos []TreeInfo
	for _, t := range tree {
		treeInfos = append(treeInfos, TreeInfo{
			PublicKey: hex.EncodeToString(t.Key),
			Parent:    hex.EncodeToString(t.Parent),
			Sequence:  t.Sequence,
		})
	}

	info := TopologyInfo{
		OurIPv6:      yggCore.Address().String(),
		OurPublicKey: hex.EncodeToString(yggCore.PublicKey()),
		Peers:        peerInfos,
		Tree:         treeInfos,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}
