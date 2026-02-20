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

const validPeerId = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const invalidHexPeerId = "gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg"

func resetMCPSessions(t *testing.T) {
	t.Helper()
	mcpSessionMutex.Lock()
	mcpSessions = map[string]bool{}
	mcpSessionMutex.Unlock()
}

// resetMCPDial restores the mcpDial var after the test.
func resetMCPDial(t *testing.T) {
	t.Helper()
	original := mcpDial
	t.Cleanup(func() { mcpDial = original })
}

// setMCPDialer overrides mcpDial to return the given conn/error pair.
func setMCPDialer(t *testing.T, conn net.Conn, err error) {
	t.Helper()
	resetMCPDial(t)
	mcpDial = func(_ *stack.Stack, _ int, _ string) (net.Conn, error) {
		return conn, err
	}
}

func TestHandleMCPInvalidPath(t *testing.T) {
	resetMCPSessions(t)
	handler := HandleMCP(7000, nil)

	req := httptest.NewRequest(http.MethodPost, "/mcp/weather", strings.NewReader("{}"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPMethodNotAllowed(t *testing.T) {
	resetMCPSessions(t)
	handler := HandleMCP(7000, nil)

	req := httptest.NewRequest(http.MethodGet, "/mcp/"+validPeerId+"/weather", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPInvalidJSON(t *testing.T) {
	resetMCPSessions(t)
	handler := HandleMCP(7000, nil)

	req := httptest.NewRequest(http.MethodPost, "/mcp/"+validPeerId+"/weather", strings.NewReader("not-json"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPNotificationsInitialized(t *testing.T) {
	resetMCPSessions(t)
	handler := HandleMCP(7000, nil)

	body := strings.NewReader(`{"method":"notifications/initialized"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+validPeerId+"/weather", body)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPInvalidSession(t *testing.T) {
	resetMCPSessions(t)
	handler := HandleMCP(7000, nil)

	body := strings.NewReader(`{"method":"call","id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+validPeerId+"/weather", body)
	req.Header.Set("Mcp-Session-Id", "missing")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPDialFailure(t *testing.T) {
	resetMCPSessions(t)
	handler := HandleMCP(7000, nil)

	body := strings.NewReader(`{"method":"initialize","id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+invalidHexPeerId+"/weather", body)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPDialFailureOnInitialize(t *testing.T) {
	resetMCPSessions(t)
	setMCPDialer(t, nil, &net.OpError{Op: "dial", Err: net.UnknownNetworkError("unreachable")})

	handler := HandleMCP(7000, nil)
	body := strings.NewReader(`{"method":"initialize","id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+validPeerId+"/weather", body)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPWriteError(t *testing.T) {
	resetMCPSessions(t)

	// Closing the peer side immediately causes the handler's write to fail.
	peerSide, handlerSide := net.Pipe()
	peerSide.Close()
	setMCPDialer(t, handlerSide, nil)

	handler := HandleMCP(7000, nil)
	body := strings.NewReader(`{"method":"initialize","id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+validPeerId+"/weather", body)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPReadError(t *testing.T) {
	resetMCPSessions(t)

	peerSide, handlerSide := net.Pipe()
	setMCPDialer(t, handlerSide, nil)

	// Drain the handler's write then close so the handler's read fails.
	go func() {
		ReadLengthPrefixed(peerSide)
		peerSide.Close()
	}()

	handler := HandleMCP(7000, nil)
	body := strings.NewReader(`{"method":"initialize","id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+validPeerId+"/weather", body)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPInvalidResponseFromPeer(t *testing.T) {
	resetMCPSessions(t)

	peerSide, handlerSide := net.Pipe()
	setMCPDialer(t, handlerSide, nil)

	go func() {
		ReadLengthPrefixed(peerSide)
		WriteLengthPrefixed(peerSide, []byte("not valid json"))
		peerSide.Close()
	}()

	handler := HandleMCP(7000, nil)
	body := strings.NewReader(`{"method":"initialize","id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+validPeerId+"/weather", body)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Result().StatusCode)
	}
}

func TestHandleMCPErrorResponseFromPeer(t *testing.T) {
	resetMCPSessions(t)

	peerSide, handlerSide := net.Pipe()
	setMCPDialer(t, handlerSide, nil)

	go func() {
		ReadLengthPrefixed(peerSide)
		errResp := MCPResponse{Service: "weather", Error: "service unavailable"}
		respBytes, _ := json.Marshal(errResp)
		WriteLengthPrefixed(peerSide, respBytes)
		peerSide.Close()
	}()

	handler := HandleMCP(7000, nil)
	body := strings.NewReader(`{"method":"initialize","id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+validPeerId+"/weather", body)
	w := httptest.NewRecorder()

	handler(w, req)

	// Handler returns JSON-RPC error (200 with error body) not an HTTP error status.
	resp := w.Result()
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("expected application/json, got %s", resp.Header.Get("Content-Type"))
	}
	var jsonrpcResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&jsonrpcResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if jsonrpcResp["error"] == nil {
		t.Error("expected 'error' field in JSON-RPC response")
	}
}

func TestHandleMCPInitializeSuccess(t *testing.T) {
	resetMCPSessions(t)

	peerSide, handlerSide := net.Pipe()
	t.Cleanup(func() { peerSide.Close(); handlerSide.Close() })
	setMCPDialer(t, handlerSide, nil)

	innerResp := json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"capabilities":{}}}`)

	go func() {
		data, err := ReadLengthPrefixed(peerSide)
		if err != nil {
			return
		}
		var env MCPMessage
		if err := json.Unmarshal(data, &env); err != nil {
			return
		}
		resp := MCPResponse{Service: env.Service, Response: innerResp}
		respBytes, _ := json.Marshal(resp)
		WriteLengthPrefixed(peerSide, respBytes)
		peerSide.Close()
	}()

	handler := HandleMCP(7000, nil)
	body := strings.NewReader(`{"method":"initialize","id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+validPeerId+"/weather", body)
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("expected Mcp-Session-Id header to be set")
	}
	mcpSessionMutex.RLock()
	if !mcpSessions[sessionID] {
		t.Errorf("session %s not found in mcpSessions", sessionID)
	}
	mcpSessionMutex.RUnlock()

	var got json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if string(got) != string(innerResp) {
		t.Errorf("expected body %s, got %s", string(innerResp), string(got))
	}
}

func TestHandleMCPDeleteSession(t *testing.T) {
	resetMCPSessions(t)

	sessionID := "test-session-delete-123"
	mcpSessionMutex.Lock()
	mcpSessions[sessionID] = true
	mcpSessionMutex.Unlock()

	handler := HandleMCP(7000, nil)
	req := httptest.NewRequest(http.MethodDelete, "/mcp/"+validPeerId+"/weather", nil)
	req.Header.Set("Mcp-Session-Id", sessionID)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Result().StatusCode)
	}
	mcpSessionMutex.RLock()
	if mcpSessions[sessionID] {
		t.Error("expected session to be removed after DELETE")
	}
	mcpSessionMutex.RUnlock()
}
