package api

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"example.com/internal/tcp/dial"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type peerConn interface {
	io.Writer
	io.Closer
}

var dialPeerConnection = func(netStack *stack.Stack, tcpPort int, peerKeyHex string) (peerConn, error) {
	return dial.DialPeerConnection(netStack, tcpPort, peerKeyHex, 0*time.Second)
}

// SendRequest is what Python sends to /send
type SendRequest struct {
	DestinationKey string `json:"destination_key"` // Hex-encoded public key
	Data           []byte `json:"data"`
}

func HandleSend(TCPPort int, netStack *stack.Stack) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		// Read raw binary body directly (no JSON/base64 decoding)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read body: %v", err), http.StatusBadRequest)
			return
		}

		conn, err := dialPeerConnection(netStack, TCPPort, destKeyHex)
		if err != nil {
			switch {
			case errors.Is(err, dial.ErrInvalidPeerKey):
				http.Error(w, "Invalid destination key", http.StatusBadRequest)
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
