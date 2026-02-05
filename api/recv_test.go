package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func resetRecvQueue() {
	RecvMutex.Lock()
	defer RecvMutex.Unlock()
	RecvQueue = nil
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

	msg := ReceivedMessage{FromKey: "deadbeef", Data: []byte("hello")}
	RecvMutex.Lock()
	RecvQueue = append(RecvQueue, msg)
	RecvMutex.Unlock()

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

	if got := resp.Header.Get("X-From-Key"); got != msg.FromKey {
		t.Fatalf("expected X-From-Key %q, got %q", msg.FromKey, got)
	}

	if string(body) != string(msg.Data) {
		t.Fatalf("expected body %q, got %q", string(msg.Data), string(body))
	}

	RecvMutex.Lock()
	remaining := len(RecvQueue)
	RecvMutex.Unlock()

	if remaining != 0 {
		t.Fatalf("expected queue to be empty, got %d items", remaining)
	}
}
