package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"github.com/gensyn-ai/axl/internal/tcp/dial"
)

var (
	// MCP session tracking for Streamable HTTP transport
	mcpSessions     = map[string]bool{}
	mcpSessionMutex sync.RWMutex
)

var mcpDial = func(netStack *stack.Stack, tcpPort int, peerId string) (net.Conn, error) {
	return dial.DialPeerConnection(netStack, tcpPort, peerId, 30*time.Second)
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

// handleMCP implements the MCP Streamable HTTP transport.
// URL format: /mcp/{peer_id}/{service}
// Claude Code connects here as a remote MCP server via HTTP transport.
func HandleMCP(TCPPort int, netStack *stack.Stack) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse path: /mcp/{peer_id}/{service}
		path := strings.TrimPrefix(r.URL.Path, "/mcp/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, "URL must be /mcp/{peer_id}/{service}", http.StatusBadRequest)
			return
		}
		peerId := parts[0]
		service := parts[1]

		switch r.Method {
		case "POST":
			handleMCPPost(w, r, service, peerId, TCPPort, netStack)
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
}

func handleMCPPost(
	w http.ResponseWriter,
	r *http.Request,
	service string,
	peerId string,
	TCPPort int,
	netStack *stack.Stack,
) {
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
		sessionID = fmt.Sprintf("mcp-%s-%s-%d", service, peerId[:8], time.Now().UnixNano())
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
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal MCP envelope: %v", err), http.StatusInternalServerError)
		return
	}
	conn, err := mcpDial(netStack, TCPPort, peerId)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to reach peer: %v", err), http.StatusBadGateway)
		return
	}
	defer conn.Close()

	// Send length-prefixed envelope
	err = WriteLengthPrefixed(conn, envelopeBytes)
	if err != nil {
		http.Error(w, "Failed to send to peer", http.StatusBadGateway)
		return
	}

	// Read the response from the peer
	respBuf, err := ReadLengthPrefixed(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
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
