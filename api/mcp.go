package api

// import (
// 	"encoding/binary"
// 	"encoding/hex"
// 	"encoding/json"
// 	"fmt"
// 	"io"
// 	"net/http"
// 	"strings"
// 	"time"

// 	"github.com/yggdrasil-network/yggdrasil-go/src/address"
// 	"gvisor.dev/gvisor/pkg/tcpip"
// 	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
// 	"gvisor.dev/gvisor/pkg/tcpip/header"
// 	"gvisor.dev/gvisor/pkg/tcpip/stack"
// )

// // handleMCP implements the MCP Streamable HTTP transport.
// // URL format: /mcp/{service}/{peer_key}
// // Claude Code connects here as a remote MCP server via HTTP transport.
// func HandleMCP(TCPPort int, netStack *stack.Stack) http.HandlerFunc {
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		// Parse path: /mcp/{service}/{peer_key}
// 		path := strings.TrimPrefix(r.URL.Path, "/mcp/")
// 		parts := strings.SplitN(path, "/", 2)
// 		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
// 			http.Error(w, "URL must be /mcp/{service}/{peer_key}", http.StatusBadRequest)
// 			return
// 		}
// 		service := parts[0]
// 		peerKeyHex := parts[1]

// 		switch r.Method {
// 		case "POST":
// 			handleMCPPost(w, r, service, peerKeyHex)
// 		case "DELETE":
// 			// Session termination
// 			sessionID := r.Header.Get("Mcp-Session-Id")
// 			if sessionID != "" {
// 				mcpSessionMutex.Lock()
// 				delete(mcpSessions, sessionID)
// 				mcpSessionMutex.Unlock()
// 			}
// 			w.WriteHeader(http.StatusAccepted)
// 		default:
// 			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
// 		}
// 	}
// }

// func handleMCPPost(w http.ResponseWriter, r *http.Request, service string, peerKeyHex string) {
// 	// Read the JSON-RPC request body
// 	body, err := io.ReadAll(r.Body)
// 	if err != nil {
// 		http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusBadRequest)
// 		return
// 	}

// 	// Parse to check the method (we need to handle initialize locally for session management)
// 	var jsonrpcReq struct {
// 		Method string `json:"method"`
// 		ID     any    `json:"id"`
// 	}
// 	if err := json.Unmarshal(body, &jsonrpcReq); err != nil {
// 		http.Error(w, "Invalid JSON-RPC", http.StatusBadRequest)
// 		return
// 	}

// 	// Handle notifications/initialized locally (no response needed)
// 	if jsonrpcReq.Method == "notifications/initialized" {
// 		w.WriteHeader(http.StatusAccepted)
// 		return
// 	}

// 	// Handle initialize locally - create session and forward to peer
// 	sessionID := r.Header.Get("Mcp-Session-Id")
// 	if jsonrpcReq.Method == "initialize" {
// 		// Generate session ID
// 		sessionID = fmt.Sprintf("mcp-%s-%s-%d", service, peerKeyHex[:8], time.Now().UnixNano())
// 		mcpSessionMutex.Lock()
// 		mcpSessions[sessionID] = true
// 		mcpSessionMutex.Unlock()
// 	} else if sessionID != "" {
// 		// Validate existing session
// 		mcpSessionMutex.RLock()
// 		valid := mcpSessions[sessionID]
// 		mcpSessionMutex.RUnlock()
// 		if !valid {
// 			http.Error(w, "Invalid or expired session", http.StatusNotFound)
// 			return
// 		}
// 	}

// 	// Wrap the JSON-RPC request in our MCP envelope and send to peer
// 	envelope := MCPMessage{
// 		Service: service,
// 		Request: body,
// 	}
// 	envelopeBytes, _ := json.Marshal(envelope)

// 	// Decode peer key
// 	destKeyBytes, err := hex.DecodeString(peerKeyHex)
// 	if err != nil || len(destKeyBytes) != 32 {
// 		http.Error(w, "Invalid peer key", http.StatusBadRequest)
// 		return
// 	}
// 	var keyArr [32]byte
// 	copy(keyArr[:], destKeyBytes)
// 	destAddr := address.AddrForKey(keyArr[:])

// 	// Dial the remote peer
// 	destIP := tcpip.AddrFromSlice(destAddr[:])
// 	conn, err := gonet.DialTCP(netStack, tcpip.FullAddress{
// 		NIC:  0,
// 		Addr: destIP,
// 		Port: uint16(TCPPort),
// 	}, header.IPv6ProtocolNumber)
// 	if err != nil {
// 		http.Error(w, fmt.Sprintf("Failed to reach peer: %v", err), http.StatusBadGateway)
// 		return
// 	}
// 	defer conn.Close()

// 	// Send length-prefixed envelope
// 	lenBuf := make([]byte, 4)
// 	binary.BigEndian.PutUint32(lenBuf, uint32(len(envelopeBytes)))
// 	if _, err := conn.Write(lenBuf); err != nil {
// 		http.Error(w, "Failed to send to peer", http.StatusBadGateway)
// 		return
// 	}
// 	if _, err := conn.Write(envelopeBytes); err != nil {
// 		http.Error(w, "Failed to send to peer", http.StatusBadGateway)
// 		return
// 	}

// 	// Read the response from the peer
// 	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

// 	respLenBuf := make([]byte, 4)
// 	if _, err := io.ReadFull(conn, respLenBuf); err != nil {
// 		http.Error(w, "No response from peer", http.StatusGatewayTimeout)
// 		return
// 	}
// 	respLen := binary.BigEndian.Uint32(respLenBuf)
// 	respBuf := make([]byte, respLen)
// 	if _, err := io.ReadFull(conn, respBuf); err != nil {
// 		http.Error(w, "Failed to read peer response", http.StatusBadGateway)
// 		return
// 	}

// 	// Parse the MCPResponse envelope to extract the inner JSON-RPC response
// 	var mcpResp MCPResponse
// 	if err := json.Unmarshal(respBuf, &mcpResp); err != nil {
// 		http.Error(w, "Invalid response from peer", http.StatusBadGateway)
// 		return
// 	}

// 	if mcpResp.Error != "" {
// 		// Return a JSON-RPC error
// 		errResp := map[string]any{
// 			"jsonrpc": "2.0",
// 			"id":      jsonrpcReq.ID,
// 			"error":   map[string]any{"code": -32603, "message": mcpResp.Error},
// 		}
// 		w.Header().Set("Content-Type", "application/json")
// 		if sessionID != "" {
// 			w.Header().Set("Mcp-Session-Id", sessionID)
// 		}
// 		json.NewEncoder(w).Encode(errResp)
// 		return
// 	}

// 	// Return the inner JSON-RPC response directly
// 	w.Header().Set("Content-Type", "application/json")
// 	if sessionID != "" {
// 		w.Header().Set("Mcp-Session-Id", sessionID)
// 	}
// 	w.Write(mcpResp.Response)
// }
