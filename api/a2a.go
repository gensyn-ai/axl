package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"example.com/internal/tcp/dial"
)

// A2AMessage is the envelope for A2A requests over Yggdrasil TCP
type A2AMessage struct {
	A2A     bool            `json:"a2a"`
	Request json.RawMessage `json:"request"` // full A2A JSON-RPC payload
}

// A2AResponse is the envelope for A2A responses over Yggdrasil TCP
type A2AResponse struct {
	A2A      bool            `json:"a2a"`
	Response json.RawMessage `json:"response"`
	Error    string          `json:"error,omitempty"`
}

// HandleA2A handles outbound A2A requests to remote peers.
// URL format: /a2a/{peer_id}
// A local A2A client POSTs a JSON-RPC request here, which gets wrapped
// in an A2AMessage envelope, sent to the remote peer over Yggdrasil TCP,
// and the A2AResponse is unwrapped and returned.
func HandleA2A(tcpPort int, netStack *stack.Stack) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse path: /a2a/{peer_id}
		peerId := strings.TrimPrefix(r.URL.Path, "/a2a/")
		if peerId == "" {
			http.Error(w, "URL must be /a2a/{peer_id}", http.StatusBadRequest)
			return
		}

		// Read the JSON-RPC request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusBadRequest)
			return
		}

		// Wrap in A2A envelope
		envelope := A2AMessage{
			A2A:     true,
			Request: body,
		}
		envelopeBytes, err := json.Marshal(envelope)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to marshal A2A envelope: %v", err), http.StatusInternalServerError)
			return
		}

		// Dial the remote peer
		conn, err := dial.DialPeerConnection(netStack, tcpPort, peerId, 30*time.Second)
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
