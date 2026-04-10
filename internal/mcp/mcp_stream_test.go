package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gensyn-ai/axl/api"
)

func TestNewMCPStream(t *testing.T) {
	routerURL := "http://localhost:8080/mcp-router"
	stream := NewMCPStream(routerURL)

	if stream == nil {
		t.Fatal("NewMCPStream returned nil")
	}
	if stream.ID != "mcp" {
		t.Errorf("expected ID 'mcp', got %s", stream.ID)
	}
	if stream.routerURL != routerURL {
		t.Errorf("expected routerURL %s, got %s", routerURL, stream.routerURL)
	}
	if stream.client == nil {
		t.Error("expected http client to be initialized")
	}
}


func TestMCPStreamIsAllowed(t *testing.T) {
	stream := NewMCPStream("http://localhost:8080")

	tests := []struct {
		name     string
		data     []byte
		mcpMsg   any
		expected bool
	}{
		{
			name:     "valid MCP message with service",
			data:     []byte(`{"service":"weather","request":{}}`),
			mcpMsg:   &api.MCPMessage{},
			expected: true,
		},
		{
			name:     "MCP message without service",
			data:     []byte(`{"request":{}}`),
			mcpMsg:   &api.MCPMessage{},
			expected: false,
		},
		{
			name:     "invalid JSON",
			data:     []byte(`not valid json`),
			mcpMsg:   &api.MCPMessage{},
			expected: false,
		},
		{
			name:     "wrong type - not pointer to MCPMessage",
			data:     []byte(`{"service":"weather"}`),
			mcpMsg:   api.MCPMessage{},
			expected: false,
		},
		{
			name:     "nil mcpMsg",
			data:     []byte(`{"service":"weather"}`),
			mcpMsg:   nil,
			expected: false,
		},
		{
			name:     "wrong pointer type",
			data:     []byte(`{"service":"weather"}`),
			mcpMsg:   &struct{}{},
			expected: false,
		},
		{
			name:     "empty service string",
			data:     []byte(`{"service":"","request":{}}`),
			mcpMsg:   &api.MCPMessage{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stream.IsAllowed(tt.data, tt.mcpMsg)
			if result != tt.expected {
				t.Errorf("IsAllowed() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestMCPStreamForwardSuccess(t *testing.T) {
	expectedService := "weather"
	expectedFromPeerId := "frompeerid123"
	expectedRequest := `{"method":"tools/call"}`
	expectedResponse := json.RawMessage(`{"result":"sunny"}`)

	var gotFromPeerId, gotService string
	var gotRequest json.RawMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RouterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		gotFromPeerId = req.FromPeerId
		gotService = req.Service
		gotRequest = req.Request

		resp := RouterResponse{Response: expectedResponse}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	stream := &MCPStream{
		ID:        "mcp",
		client:    server.Client(),
		routerURL: server.URL,
	}

	mcpMsg := &api.MCPMessage{
		Service: expectedService,
		Request: json.RawMessage(expectedRequest),
	}

	respBytes, err := stream.Forward(mcpMsg, expectedFromPeerId)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	var mcpResp api.MCPResponse
	if err := json.Unmarshal(respBytes, &mcpResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Verify response fields
	if mcpResp.Service != expectedService {
		t.Errorf("expected service %q, got %q", expectedService, mcpResp.Service)
	}
	if string(mcpResp.Response) != string(expectedResponse) {
		t.Errorf("expected response %s, got %s", string(expectedResponse), string(mcpResp.Response))
	}
	if mcpResp.Error != "" {
		t.Errorf("expected no error, got %s", mcpResp.Error)
	}

	// Verify the router received the correct routing fields
	if gotFromPeerId != expectedFromPeerId {
		t.Errorf("expected fromPeerId %q forwarded to router, got %q", expectedFromPeerId, gotFromPeerId)
	}
	if gotService != expectedService {
		t.Errorf("expected service %q forwarded to router, got %q", expectedService, gotService)
	}
	if string(gotRequest) != expectedRequest {
		t.Errorf("expected request %s forwarded to router, got %s", expectedRequest, string(gotRequest))
	}
}

func TestMCPStreamForwardWrongType(t *testing.T) {
	stream := NewMCPStream("http://localhost:8080")

	// Pass wrong type (not a pointer to MCPMessage)
	respBytes, err := stream.Forward("wrong type", "frompeerid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if respBytes != nil {
		t.Errorf("expected nil response for wrong type, got %s", string(respBytes))
	}
}

func TestMCPStreamForwardNilMessage(t *testing.T) {
	stream := NewMCPStream("http://localhost:8080")

	respBytes, err := stream.Forward(nil, "frompeerid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if respBytes != nil {
		t.Errorf("expected nil response for nil message, got %s", string(respBytes))
	}
}

func TestMCPStreamForwardRouterError(t *testing.T) {
	// Create mock router that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := RouterResponse{
			Error: "service unavailable",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	stream := &MCPStream{
		ID:        "mcp",
		client:    server.Client(),
		routerURL: server.URL,
	}

	mcpMsg := &api.MCPMessage{
		Service: "weather",
		Request: json.RawMessage(`{}`),
	}

	respBytes, err := stream.Forward(mcpMsg, "frompeerid123")
	if err != nil {
		t.Fatalf("Forward should not return error, got: %v", err)
	}

	var mcpResp api.MCPResponse
	if err := json.Unmarshal(respBytes, &mcpResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if mcpResp.Error == "" {
		t.Error("expected error in response, got empty")
	}
	if mcpResp.Service != "weather" {
		t.Errorf("expected service 'weather', got %s", mcpResp.Service)
	}
}

func TestMCPStreamForwardRouterConnectionFailure(t *testing.T) {
	// Close the server immediately so any request gets "connection refused".
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	stream := &MCPStream{
		ID:        "mcp",
		client:    server.Client(),
		routerURL: server.URL,
	}

	mcpMsg := &api.MCPMessage{
		Service: "weather",
		Request: json.RawMessage(`{}`),
	}

	respBytes, err := stream.Forward(mcpMsg, "frompeerid123")
	if err != nil {
		t.Fatalf("Forward should not return error directly, got: %v", err)
	}

	var mcpResp api.MCPResponse
	if err := json.Unmarshal(respBytes, &mcpResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Should return an error in the MCPResponse
	if mcpResp.Error == "" {
		t.Error("expected error in response for connection failure")
	}
}

func TestMCPStreamForwardNullResponse(t *testing.T) {
	// Create mock router that returns null as response
	// Note: JSON null unmarshals to []byte("null"), not nil
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := RouterResponse{
			Response: nil, // Encoded as JSON null
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	stream := &MCPStream{
		ID:        "mcp",
		client:    server.Client(),
		routerURL: server.URL,
	}

	mcpMsg := &api.MCPMessage{
		Service: "weather",
		Request: json.RawMessage(`{}`),
	}

	respBytes, err := stream.Forward(mcpMsg, "frompeerid123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// JSON null becomes []byte("null") which is not nil,
	// so Forward will return a marshaled response
	var mcpResp api.MCPResponse
	if err := json.Unmarshal(respBytes, &mcpResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if mcpResp.Service != "weather" {
		t.Errorf("expected service 'weather', got %s", mcpResp.Service)
	}
	// Response will be null (the JSON literal)
	if string(mcpResp.Response) != "null" {
		t.Errorf("expected 'null' response, got %s", string(mcpResp.Response))
	}
}

