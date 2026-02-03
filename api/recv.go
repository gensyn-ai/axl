package api

import (
	"net/http"
	"sync"
)

// ReceivedMessage holds incoming data with sender info
type ReceivedMessage struct {
	FromKey string `json:"from_key"`
	Data    []byte `json:"data"`
}

var (
	RecvMutex sync.Mutex
	RecvQueue []ReceivedMessage
)

func HandleRecv(w http.ResponseWriter, r *http.Request) {
	RecvMutex.Lock()
	defer RecvMutex.Unlock()

	if len(RecvQueue) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Pop first message
	msg := RecvQueue[0]
	RecvQueue = RecvQueue[1:]

	// Return raw binary with sender key in header (no JSON/base64)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-From-Key", msg.FromKey)
	w.Write(msg.Data)
}
