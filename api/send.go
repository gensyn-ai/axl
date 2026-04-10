package api

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gensyn-ai/axl/internal/tcp/dial"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type peerConn interface {
	io.Writer
	io.Closer
}

var dialPeerConnection = func(netStack *stack.Stack, tcpPort int, peerId string) (peerConn, error) {
	return dial.DialPeerConnection(netStack, tcpPort, peerId, 0*time.Second)
}

// SendRequest is what Python sends to /send
type SendRequest struct {
	DestinationPeerId string `json:"destination_peer_id"` // Hex-encoded peer ID
	Data              []byte `json:"data"`
}

func HandleSend(TCPPort int, netStack *stack.Stack) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Get destination peer ID from header (raw binary, no JSON/base64)
		destPeerId := r.Header.Get("X-Destination-Peer-Id")
		if destPeerId == "" {
			http.Error(w, "Missing X-Destination-Peer-Id header", http.StatusBadRequest)
			return
		}

		// Read raw binary body directly (no JSON/base64 decoding)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusBadRequest)
			return
		}

		conn, err := dialPeerConnection(netStack, TCPPort, destPeerId)
		if err != nil {
			switch {
			case errors.Is(err, dial.ErrInvalidPeerId):
				http.Error(w, "Invalid destination peer ID", http.StatusBadRequest)
			case errors.Is(err, dial.ErrDialPeer):
				http.Error(w, fmt.Sprintf("Failed to reach peer: %v", err), http.StatusBadGateway)
			default:
				http.Error(w, fmt.Sprintf("Dial failed: %v", err), http.StatusInternalServerError)
			}
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

		// Return minimal response immediately; MCP traffic uses a separate endpoint.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Sent-Bytes", fmt.Sprintf("%d", len(data)))
		w.WriteHeader(http.StatusOK)
	}
}
