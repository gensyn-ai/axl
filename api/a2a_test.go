package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// resetA2ADial restores the a2aDial var after the test.
func resetA2ADial(t *testing.T) {
	t.Helper()
	original := a2aDial
	t.Cleanup(func() { a2aDial = original })
}

// setA2ADialer overrides a2aDial to return the given conn/error pair.
func setA2ADialer(t *testing.T, conn net.Conn, err error) {
	t.Helper()
	resetA2ADial(t)
	a2aDial = func(_ *stack.Stack, _ int, _ string) (net.Conn, error) {
		return conn, err
	}
}

func TestHandleA2AEmptyPeerId(t *testing.T) {
	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/a2a/", strings.NewReader("{}"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleA2AMethodNotAllowed(t *testing.T) {
	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPut, "/a2a/"+validPeerId, nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Result().StatusCode)
	}
}

func TestHandleA2ABodyReadError(t *testing.T) {
	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/a2a/"+validPeerId, failingReader{})
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleA2ADialFailure(t *testing.T) {
	setA2ADialer(t, nil, &net.OpError{Op: "dial", Err: net.UnknownNetworkError("unreachable")})

	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/a2a/"+validPeerId, strings.NewReader("{}"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleA2AWriteError(t *testing.T) {
	peerSide, handlerSide := net.Pipe()
	peerSide.Close() // handler's write will immediately fail
	setA2ADialer(t, handlerSide, nil)

	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/a2a/"+validPeerId, strings.NewReader("{}"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleA2AReadError(t *testing.T) {
	peerSide, handlerSide := net.Pipe()
	setA2ADialer(t, handlerSide, nil)

	// Drain the write then close so the handler's read fails.
	go func() {
		ReadLengthPrefixed(peerSide)
		peerSide.Close()
	}()

	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/a2a/"+validPeerId, strings.NewReader("{}"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleA2AInvalidResponseJSON(t *testing.T) {
	peerSide, handlerSide := net.Pipe()
	setA2ADialer(t, handlerSide, nil)

	go func() {
		ReadLengthPrefixed(peerSide)
		WriteLengthPrefixed(peerSide, []byte("not valid json"))
		peerSide.Close()
	}()

	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/a2a/"+validPeerId, strings.NewReader("{}"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleA2AErrorResponseFromPeer(t *testing.T) {
	peerSide, handlerSide := net.Pipe()
	setA2ADialer(t, handlerSide, nil)

	go func() {
		ReadLengthPrefixed(peerSide)
		errResp := A2AResponse{A2A: true, Error: "agent offline"}
		respBytes, _ := json.Marshal(errResp)
		WriteLengthPrefixed(peerSide, respBytes)
		peerSide.Close()
	}()

	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/a2a/"+validPeerId, strings.NewReader("{}"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleA2APOSTSuccess(t *testing.T) {
	expectedPayload := json.RawMessage(`{"jsonrpc":"2.0","result":{"id":"task-1"},"id":1}`)

	peerSide, handlerSide := net.Pipe()
	t.Cleanup(func() { peerSide.Close(); handlerSide.Close() })
	setA2ADialer(t, handlerSide, nil)

	requestBody := `{"jsonrpc":"2.0","method":"message/send","id":1}`

	go func() {
		data, err := ReadLengthPrefixed(peerSide)
		if err != nil {
			return
		}
		// Verify the envelope contains the request body
		var env A2AMessage
		if err := json.Unmarshal(data, &env); err != nil {
			return
		}
		resp := A2AResponse{A2A: true, Response: expectedPayload}
		respBytes, _ := json.Marshal(resp)
		WriteLengthPrefixed(peerSide, respBytes)
		peerSide.Close()
	}()

	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/a2a/"+validPeerId, strings.NewReader(requestBody))
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("expected application/json, got %s", resp.Header.Get("Content-Type"))
	}
	var got json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if string(got) != string(expectedPayload) {
		t.Errorf("expected body %s, got %s", string(expectedPayload), string(got))
	}
}

func TestHandleA2AGETSuccess(t *testing.T) {
	agentCard := json.RawMessage(`{"name":"test-agent","url":"http://example.com"}`)

	peerSide, handlerSide := net.Pipe()
	t.Cleanup(func() { peerSide.Close(); handlerSide.Close() })
	setA2ADialer(t, handlerSide, nil)

	go func() {
		data, err := ReadLengthPrefixed(peerSide)
		if err != nil {
			return
		}
		// Verify the envelope has AgentCard: true
		var env A2AMessage
		if err := json.Unmarshal(data, &env); err != nil {
			return
		}
		if !env.AgentCard {
			return
		}
		resp := A2AResponse{A2A: true, Response: agentCard}
		respBytes, _ := json.Marshal(resp)
		WriteLengthPrefixed(peerSide, respBytes)
		peerSide.Close()
	}()

	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodGet, "/a2a/"+validPeerId, nil)
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if string(got) != string(agentCard) {
		t.Errorf("expected body %s, got %s", string(agentCard), string(got))
	}
}

func TestHandleA2APOSTEnvelopeContainsRequestBody(t *testing.T) {
	requestBody := `{"jsonrpc":"2.0","method":"message/send","params":{"message":{"role":"user"}},"id":1}`

	peerSide, handlerSide := net.Pipe()
	t.Cleanup(func() { peerSide.Close(); handlerSide.Close() })
	setA2ADialer(t, handlerSide, nil)

	envelopeCh := make(chan A2AMessage, 1)
	go func() {
		data, err := ReadLengthPrefixed(peerSide)
		if err != nil {
			close(envelopeCh)
			return
		}
		var env A2AMessage
		json.Unmarshal(data, &env)
		envelopeCh <- env

		resp := A2AResponse{A2A: true, Response: json.RawMessage(`{}`)}
		respBytes, _ := json.Marshal(resp)
		WriteLengthPrefixed(peerSide, respBytes)
		peerSide.Close()
	}()

	handler := HandleA2A(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/a2a/"+validPeerId, strings.NewReader(requestBody))
	w := httptest.NewRecorder()

	handler(w, req)

	env := <-envelopeCh
	if !env.A2A {
		t.Error("expected A2A flag to be true in envelope")
	}
	if env.AgentCard {
		t.Error("expected AgentCard to be false for POST")
	}
	if string(env.Request) != requestBody {
		t.Errorf("expected request body %s, got %s", requestBody, string(env.Request))
	}
}
