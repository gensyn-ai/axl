package api

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"

	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

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

		// Return minimal response immediately; MCP traffic uses a separate endpoint.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Sent-Bytes", fmt.Sprintf("%d", len(data)))
		w.WriteHeader(http.StatusOK)
	}
}
