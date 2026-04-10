package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"github.com/gensyn-ai/axl/internal/tcp/dial"
)

var a2aDial = func(netStack *stack.Stack, tcpPort int, peerId string) (net.Conn, error) {
	return dial.DialPeerConnection(netStack, tcpPort, peerId, 30*time.Second)
}

// A2AMessage is the envelope for A2A requests over Yggdrasil TCP
type A2AMessage struct {
	A2A       bool            `json:"a2a"`
	AgentCard bool            `json:"agent_card,omitempty"` // if true, fetch the remote agent card instead of forwarding a JSON-RPC request
	Request   json.RawMessage `json:"request"`              // full A2A JSON-RPC payload
}

// A2AResponse is the envelope for A2A responses over Yggdrasil TCP
type A2AResponse struct {
	A2A      bool            `json:"a2a"`
	Response json.RawMessage `json:"response"`
	Error    string          `json:"error,omitempty"`
}

// HandleA2A handles outbound A2A requests to remote peers.
// URL format: /a2a/{peer_id}
// POST: forwards a JSON-RPC request to the remote peer's A2A server.
// GET:  fetches the remote peer's agent card (/.well-known/agent-card.json) for discovery.
func HandleA2A(tcpPort int, netStack *stack.Stack) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse path: /a2a/{peer_id}
		peerId := strings.TrimPrefix(r.URL.Path, "/a2a/")
		if peerId == "" {
			http.Error(w, "URL must be /a2a/{peer_id}", http.StatusBadRequest)
			return
		}

		var envelope A2AMessage
		switch r.Method {
		case "GET":
			// Agent card discovery — no body needed
			envelope = A2AMessage{A2A: true, AgentCard: true}
		case "POST":
			// Read the JSON-RPC request body
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusBadRequest)
				return
			}
			envelope = A2AMessage{A2A: true, Request: body}
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		envelopeBytes, err := json.Marshal(envelope)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to marshal A2A envelope: %v", err), http.StatusInternalServerError)
			return
		}

		// Dial the remote peer
		conn, err := a2aDial(netStack, tcpPort, peerId)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to reach peer: %v", err), http.StatusBadGateway)
			return
		}
		defer conn.Close()

		// Send length-prefixed envelope
		if err := WriteLengthPrefixed(conn, envelopeBytes); err != nil {
			http.Error(w, "Failed to send to peer", http.StatusBadGateway)
			return
		}

		// Read the response from the peer
		respBuf, err := ReadLengthPrefixed(conn)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		// Parse the A2AResponse envelope
		var a2aResp A2AResponse
		if err := json.Unmarshal(respBuf, &a2aResp); err != nil {
			http.Error(w, "Invalid response from peer", http.StatusBadGateway)
			return
		}

		if a2aResp.Error != "" {
			http.Error(w, a2aResp.Error, http.StatusBadGateway)
			return
		}

		// Return the inner JSON-RPC response directly
		w.Header().Set("Content-Type", "application/json")
		w.Write(a2aResp.Response)
	}
}
