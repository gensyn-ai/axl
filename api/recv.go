package api

import (
	"net/http"
	"sync"
)

// ReceivedMessage holds incoming data with sender info
type ReceivedMessage struct {
	FromPeerId string `json:"from_peer_id"`
	Data       []byte `json:"data"`
}

type receivedQueue struct {
	mu       sync.Mutex
	items    []ReceivedMessage
	capacity int
}

func newReceivedQueue(capacity int) *receivedQueue {
	return &receivedQueue{capacity: capacity}
}

func (q *receivedQueue) Push(msg ReceivedMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) >= q.capacity {
		q.items = q.items[1:]
	}
	q.items = append(q.items, msg)
}

func (q *receivedQueue) Pop() (ReceivedMessage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return ReceivedMessage{}, false
	}
	msg := q.items[0]
	q.items = q.items[1:]
	return msg, true
}

func (q *receivedQueue) Reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
}

func (q *receivedQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *receivedQueue) Snapshot() []ReceivedMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]ReceivedMessage, len(q.items))
	copy(result, q.items)
	return result
}

// DefaultRecvQueue is the shared receive queue for the /recv endpoint.
var DefaultRecvQueue = newReceivedQueue(100)

func HandleRecv(w http.ResponseWriter, r *http.Request) {
	msg, ok := DefaultRecvQueue.Pop()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Return raw binary with sender peer ID in header (no JSON/base64)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-From-Peer-Id", msg.FromPeerId)
	w.Write(msg.Data)
}
