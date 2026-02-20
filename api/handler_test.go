package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHandlerRouting(t *testing.T) {
	// Create handler with nil dependencies (we're just testing routing, not full functionality)
	// Note: topology route requires real yggCore so we skip it here
	handler := NewHandler(nil, 7000, nil)

	tests := []struct {
		name           string
		method         string
		path           string
		expectStatus   int
		expectContains string
	}{
		{
			name:         "recv route exists - empty queue returns 204",
			method:       http.MethodGet,
			path:         "/recv",
			expectStatus: http.StatusNoContent,
		},
		{
			name:         "send route exists - wrong method",
			method:       http.MethodGet,
			path:         "/send",
			expectStatus: http.StatusMethodNotAllowed,
		},
		{
			name:         "send route exists - missing header",
			method:       http.MethodPost,
			path:         "/send",
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "mcp route exists - invalid path",
			method:       http.MethodPost,
			path:         "/mcp/",
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "mcp route exists - wrong method",
			method:       http.MethodGet,
			path:         "/mcp/peer/service",
			expectStatus: http.StatusMethodNotAllowed,
		},
		{
			name:         "a2a route exists - missing peer id",
			method:       http.MethodPost,
			path:         "/a2a/",
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "a2a route exists - method not allowed",
			method:       http.MethodPut,
			path:         "/a2a/" + validPeerId,
			expectStatus: http.StatusMethodNotAllowed,
		},
		{
			name:         "unknown route returns 404",
			method:       http.MethodGet,
			path:         "/unknown",
			expectStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.expectStatus {
				t.Errorf("expected status %d, got %d", tt.expectStatus, w.Code)
			}
		})
	}
}

func TestNewHandlerMCPRouting(t *testing.T) {
	resetMCPSessions(t)
	handler := NewHandler(nil, 7000, nil)

	// Test that MCP routes are properly registered with path prefix matching
	tests := []struct {
		name   string
		path   string
		method string
		body   string
	}{
		{
			name:   "mcp with peer and service",
			path:   "/mcp/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/weather",
			method: http.MethodPost,
			body:   `{"method":"notifications/initialized"}`,
		},
		{
			name:   "mcp with different service",
			path:   "/mcp/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/calendar",
			method: http.MethodPost,
			body:   `{"method":"notifications/initialized"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// notifications/initialized returns 202 Accepted
			if w.Code != http.StatusAccepted {
				t.Errorf("expected status %d, got %d (body: %s)", http.StatusAccepted, w.Code, w.Body.String())
			}
		})
	}
}

func TestNewHandlerRecvIntegration(t *testing.T) {
	t.Cleanup(func() { DefaultRecvQueue.Reset() })

	handler := NewHandler(nil, 7000, nil)

	DefaultRecvQueue.Reset()
	DefaultRecvQueue.Push(ReceivedMessage{
		FromPeerId: "testpeerid123",
		Data:       []byte("test data"),
	})

	// Request through the handler
	req := httptest.NewRequest(http.MethodGet, "/recv", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// FromPeerId is returned in the X-From-Peer-Id header
	fromPeerId := w.Header().Get("X-From-Peer-Id")
	if fromPeerId != "testpeerid123" {
		t.Errorf("expected X-From-Peer-Id header 'testpeerid123', got %s", fromPeerId)
	}

	// Body contains the raw data
	body := w.Body.String()
	if body != "test data" {
		t.Errorf("expected body 'test data', got %s", body)
	}
}
