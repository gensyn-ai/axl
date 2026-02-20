package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func resetRecvQueue() {
	DefaultRecvQueue.Reset()
}

func TestHandleRecvEmptyQueue(t *testing.T) {
	t.Cleanup(resetRecvQueue)
	resetRecvQueue()

	req := httptest.NewRequest(http.MethodGet, "/recv", nil)
	w := httptest.NewRecorder()

	HandleRecv(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, resp.StatusCode)
	}
}

func TestHandleRecvReturnsMessage(t *testing.T) {
	t.Cleanup(resetRecvQueue)
	resetRecvQueue()

	msg := ReceivedMessage{FromPeerId: "deadbeef", Data: []byte("hello")}
	DefaultRecvQueue.Push(msg)

	req := httptest.NewRequest(http.MethodGet, "/recv", nil)
	w := httptest.NewRecorder()

	HandleRecv(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if got := resp.Header.Get("X-From-Peer-Id"); got != msg.FromPeerId {
		t.Fatalf("expected X-From-Peer-Id %q, got %q", msg.FromPeerId, got)
	}

	if string(body) != string(msg.Data) {
		t.Fatalf("expected body %q, got %q", string(msg.Data), string(body))
	}

	if DefaultRecvQueue.Len() != 0 {
		t.Fatalf("expected queue to be empty, got %d items", DefaultRecvQueue.Len())
	}
}
