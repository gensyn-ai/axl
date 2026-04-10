package listen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gensyn-ai/axl/api"
	"github.com/gensyn-ai/axl/internal/mcp"
)

func TestNewMultiplexer(t *testing.T) {
	m := NewMultiplexer()
	if m == nil {
		t.Fatal("NewMultiplexer returned nil")
	}
	if m.sources != nil && len(m.sources) != 0 {
		t.Errorf("expected empty sources, got %d", len(m.sources))
	}
	if m.requestTypes == nil {
		t.Error("expected requestTypes map to be initialized")
	}
}

func TestMultiplexerAddSource(t *testing.T) {
	m := NewMultiplexer()

	// Create a mock stream
	stream := &mockStream{id: "test-stream"}
	factory := func() any { return &struct{}{} }

	m.AddSource(stream, factory)

	if len(m.sources) != 1 {
		t.Errorf("expected 1 source, got %d", len(m.sources))
	}
	if m.sources[0].GetID() != "test-stream" {
		t.Errorf("expected stream ID 'test-stream', got %s", m.sources[0].GetID())
	}
}

func TestMultiplexerGetFactory(t *testing.T) {
	m := NewMultiplexer()

	stream := &mockStream{id: "my-stream"}
	called := false
	factory := func() any {
		called = true
		return &struct{}{}
	}

	m.AddSource(stream, factory)

	retrievedFactory := m.GetFactory("my-stream")
	if retrievedFactory == nil {
		t.Fatal("GetFactory returned nil")
	}

	retrievedFactory()
	if !called {
		t.Error("expected factory to be called")
	}
}

func TestMultiplexerGetFactoryNotFound(t *testing.T) {
	m := NewMultiplexer()

	factory := m.GetFactory("nonexistent")
	if factory != nil {
		t.Error("expected nil for nonexistent stream")
	}
}

// mockStream implements the Stream interface for testing
type mockStream struct {
	id            string
	allowedData   []byte
	forwardResult []byte
	forwardErr    error
}

func (m *mockStream) GetID() string {
	return m.id
}

func (m *mockStream) IsAllowed(data []byte, metadata any) bool {
	if m.allowedData == nil {
		return false
	}
	return string(data) == string(m.allowedData)
}

func (m *mockStream) Forward(metadata any, fromPeerId string) ([]byte, error) {
	return m.forwardResult, m.forwardErr
}

func TestMultiplexerWithMockStream(t *testing.T) {
	m := NewMultiplexer()

	stream := &mockStream{
		id:            "mock",
		allowedData:   []byte(`{"type":"mock"}`),
		forwardResult: []byte(`{"result":"ok"}`),
	}

	m.AddSource(stream, func() any { return &struct{}{} })

	// Simulate message routing like handleTCPConn does
	testData := []byte(`{"type":"mock"}`)

	for _, s := range m.sources {
		msgPtr := m.requestTypes[s.GetID()]()
		if s.IsAllowed(testData, msgPtr) {
			resp, err := s.Forward(msgPtr, "testpeerid")
			if err != nil {
				t.Fatalf("Forward error: %v", err)
			}
			if string(resp) != `{"result":"ok"}` {
				t.Errorf("expected result ok, got %s", string(resp))
			}
			return
		}
	}
	t.Error("no stream matched the data")
}

// Integration test: Multiplexer with real MCPStream
func TestMultiplexerWithMCPStream(t *testing.T) {
	// Create a mock MCP router server
	expectedResponse := json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcp.RouterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		resp := mcp.RouterResponse{
			Response: expectedResponse,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create multiplexer with real MCPStream pointing to the test server.
	m := NewMultiplexer()
	mcpStream := mcp.NewMCPStream(server.URL)
	m.AddSource(mcpStream, func() any { return &api.MCPMessage{} })

	// Simulate incoming MCP message
	mcpData := []byte(`{"service":"weather","request":{"jsonrpc":"2.0","method":"tools/list","id":1}}`)

	var matched bool
	for _, s := range m.sources {
		msgPtr := m.requestTypes[s.GetID()]()
		if s.IsAllowed(mcpData, msgPtr) {
			matched = true

			// Forward the message
			resp, err := s.Forward(msgPtr, "frompeerid123")
			if err != nil {
				t.Fatalf("Forward error: %v", err)
			}

			// Verify response
			var mcpResp api.MCPResponse
			if err := json.Unmarshal(resp, &mcpResp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if mcpResp.Service != "weather" {
				t.Errorf("expected service 'weather', got %s", mcpResp.Service)
			}
			if string(mcpResp.Response) != string(expectedResponse) {
				t.Errorf("expected response %s, got %s", string(expectedResponse), string(mcpResp.Response))
			}
			break
		}
	}

	if !matched {
		t.Error("MCPStream did not match the MCP data")
	}
}

func TestMultiplexerWithMCPStreamInvalidData(t *testing.T) {
	m := NewMultiplexer()
	mcpStream := mcp.NewMCPStream("http://localhost:9999")

	m.AddSource(mcpStream, func() any { return &api.MCPMessage{} })

	// Non-MCP data should not be matched
	nonMCPData := []byte(`{"type":"something_else"}`)

	for _, s := range m.sources {
		msgPtr := m.requestTypes[s.GetID()]()
		if s.IsAllowed(nonMCPData, msgPtr) {
			t.Error("MCPStream should not match non-MCP data")
		}
	}
}

func TestMultiplexerWithMCPStreamRouterError(t *testing.T) {
	// Create a mock router that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mcp.RouterResponse{
			Error: "service not found",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	m := NewMultiplexer()
	mcpStream := mcp.NewMCPStream(server.URL)
	m.AddSource(mcpStream, func() any { return &api.MCPMessage{} })

	mcpData := []byte(`{"service":"unknown","request":{}}`)

	for _, s := range m.sources {
		msgPtr := m.requestTypes[s.GetID()]()
		if s.IsAllowed(mcpData, msgPtr) {
			resp, err := s.Forward(msgPtr, "frompeerid")
			if err != nil {
				t.Fatalf("Forward should not return error directly: %v", err)
			}

			var mcpResp api.MCPResponse
			if err := json.Unmarshal(resp, &mcpResp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			// Error should be in the response
			if mcpResp.Error == "" {
				t.Error("expected error in response")
			}
			return
		}
	}
	t.Error("MCPStream did not match the data")
}

func TestMultiplexerMultipleStreams(t *testing.T) {
	m := NewMultiplexer()

	// Add MCP stream
	mcpStream := mcp.NewMCPStream("http://localhost:9999")
	m.AddSource(mcpStream, func() any { return &api.MCPMessage{} })

	// Add another mock stream
	otherStream := &mockStream{
		id:            "other",
		allowedData:   []byte(`{"type":"other"}`),
		forwardResult: []byte(`{"handled":"by-other"}`),
	}
	m.AddSource(otherStream, func() any { return &struct{}{} })

	if len(m.sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(m.sources))
	}

	// Test that MCP data routes to MCP stream
	mcpData := []byte(`{"service":"test","request":{}}`)
	for _, s := range m.sources {
		msgPtr := m.requestTypes[s.GetID()]()
		if s.IsAllowed(mcpData, msgPtr) {
			if s.GetID() != "mcp" {
				t.Errorf("expected MCP stream to match, got %s", s.GetID())
			}
			break
		}
	}

	// Test that other data routes to other stream
	otherData := []byte(`{"type":"other"}`)
	for _, s := range m.sources {
		msgPtr := m.requestTypes[s.GetID()]()
		if s.IsAllowed(otherData, msgPtr) {
			if s.GetID() != "other" {
				t.Errorf("expected other stream to match, got %s", s.GetID())
			}
			break
		}
	}
}
