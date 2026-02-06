package api

import (
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validPeerId = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const invalidHexPeerId = "gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg"

func resetMCPSessions(t *testing.T) {
	t.Helper()
	mcpSessionMutex.Lock()
	mcpSessions = map[string]bool{}
	mcpSessionMutex.Unlock()
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

func TestSendAndReadMCPRequestResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	expectedPayload := []byte(`{"service":"weather"}`)
	go func() {
		defer server.Close()
		defer close(done)
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(server, lenBuf); err != nil {
			t.Errorf("server failed to read len: %v", err)
			return
		}
		payloadLen := binary.BigEndian.Uint32(lenBuf)
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(server, payload); err != nil {
			t.Errorf("server failed to read payload: %v", err)
			return
		}
		if string(payload) != string(expectedPayload) {
			t.Errorf("unexpected payload %s", payload)
		}
		resp := []byte(`{"result":"ok"}`)
		respLen := make([]byte, 4)
		binary.BigEndian.PutUint32(respLen, uint32(len(resp)))
		if _, err := server.Write(respLen); err != nil {
			t.Errorf("server failed to write len: %v", err)
			return
		}
		if _, err := server.Write(resp); err != nil {
			t.Errorf("server failed to write resp: %v", err)
			return
		}
	}()

	if err := WriteLengthPrefixed(client, expectedPayload); err != nil {
		t.Fatalf("WriteLengthPrefixed failed: %v", err)
	}
	resp, err := ReadLengthPrefixed(client)
	if err != nil {
		t.Fatalf("ReadLengthPrefixed failed: %v", err)
	}
	if string(resp) != `{"result":"ok"}` {
		t.Fatalf("unexpected response %s", resp)
	}
	<-done
}
