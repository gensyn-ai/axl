package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gensyn-ai/axl/api"
)

func TestNewA2AStream(t *testing.T) {
	a2aURL := "http://localhost:9004"
	stream := NewA2AStream(a2aURL)

	if stream == nil {
		t.Fatal("NewA2AStream returned nil")
	}
	if stream.ID != "a2a" {
		t.Errorf("expected ID 'a2a', got %s", stream.ID)
	}
	if stream.a2aURL != a2aURL {
		t.Errorf("expected a2aURL %s, got %s", a2aURL, stream.a2aURL)
	}
	if stream.client == nil {
		t.Error("expected http client to be initialized")
	}
}


func TestA2AStreamIsAllowed(t *testing.T) {
	stream := NewA2AStream("http://localhost:9004")

	tests := []struct {
		name     string
		data     []byte
		a2aMsg   any
		expected bool
	}{
		{
			name:     "valid A2A message with a2a true",
			data:     []byte(`{"a2a":true,"request":{"jsonrpc":"2.0","method":"message/send"}}`),
			a2aMsg:   &api.A2AMessage{},
			expected: true,
		},
		{
			name:     "A2A message with a2a false",
			data:     []byte(`{"a2a":false,"request":{}}`),
			a2aMsg:   &api.A2AMessage{},
			expected: false,
		},
		{
			name:     "A2A message without a2a field",
			data:     []byte(`{"request":{}}`),
			a2aMsg:   &api.A2AMessage{},
			expected: false,
		},
		{
			name:     "invalid JSON",
			data:     []byte(`not valid json`),
			a2aMsg:   &api.A2AMessage{},
			expected: false,
		},
		{
			name:     "wrong type - not pointer to A2AMessage",
			data:     []byte(`{"a2a":true}`),
			a2aMsg:   api.A2AMessage{},
			expected: false,
		},
		{
			name:     "nil a2aMsg",
			data:     []byte(`{"a2a":true}`),
			a2aMsg:   nil,
			expected: false,
		},
		{
			name:     "wrong pointer type",
			data:     []byte(`{"a2a":true}`),
			a2aMsg:   &struct{}{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stream.IsAllowed(tt.data, tt.a2aMsg)
			if result != tt.expected {
				t.Errorf("IsAllowed() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestA2AStreamForwardSuccess(t *testing.T) {
	expectedResponse := json.RawMessage(`{"jsonrpc":"2.0","result":{"id":"task-123"},"id":1}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-From-Peer-Id") != "frompeerid123" {
			t.Errorf("expected X-From-Peer-Id frompeerid123, got %s", r.Header.Get("X-From-Peer-Id"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(expectedResponse)
	}))
	defer server.Close()

	stream := &A2AStream{
		ID:     "a2a",
		client: server.Client(),
		a2aURL: server.URL,
	}

	a2aMsg := &api.A2AMessage{
		A2A:     true,
		Request: json.RawMessage(`{"jsonrpc":"2.0","method":"message/send","id":1}`),
	}

	respBytes, err := stream.Forward(a2aMsg, "frompeerid123")
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	var a2aResp api.A2AResponse
	if err := json.Unmarshal(respBytes, &a2aResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !a2aResp.A2A {
		t.Error("expected A2A to be true")
	}
	if string(a2aResp.Response) != string(expectedResponse) {
		t.Errorf("expected response %s, got %s", string(expectedResponse), string(a2aResp.Response))
	}
	if a2aResp.Error != "" {
		t.Errorf("expected no error, got %s", a2aResp.Error)
	}
}

func TestA2AStreamForwardWrongType(t *testing.T) {
	stream := NewA2AStream("http://localhost:9004")

	respBytes, err := stream.Forward("wrong type", "frompeerid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if respBytes != nil {
		t.Errorf("expected nil response for wrong type, got %s", string(respBytes))
	}
}

func TestA2AStreamForwardNilMessage(t *testing.T) {
	stream := NewA2AStream("http://localhost:9004")

	respBytes, err := stream.Forward(nil, "frompeerid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if respBytes != nil {
		t.Errorf("expected nil response for nil message, got %s", string(respBytes))
	}
}

func TestA2AStreamForwardConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	stream := &A2AStream{
		ID:     "a2a",
		client: server.Client(),
		a2aURL: server.URL,
	}

	a2aMsg := &api.A2AMessage{
		A2A:     true,
		Request: json.RawMessage(`{}`),
	}

	respBytes, err := stream.Forward(a2aMsg, "frompeerid123")
	if err != nil {
		t.Fatalf("Forward should not return error directly, got: %v", err)
	}

	var a2aResp api.A2AResponse
	if err := json.Unmarshal(respBytes, &a2aResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !a2aResp.A2A {
		t.Error("expected A2A to be true")
	}
	if a2aResp.Error == "" {
		t.Error("expected error in response for connection failure")
	}
}

func TestA2AStreamForwardAgentCard(t *testing.T) {
	expectedCard := json.RawMessage(`{"name":"test-agent","url":"http://example.com"}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/.well-known/agent-card.json" {
			t.Errorf("expected path /.well-known/agent-card.json, got %s", r.URL.Path)
		}
		if r.Header.Get("X-From-Peer-Id") != "frompeerid123" {
			t.Errorf("expected X-From-Peer-Id frompeerid123, got %s", r.Header.Get("X-From-Peer-Id"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(expectedCard)
	}))
	defer server.Close()

	stream := &A2AStream{
		ID:     "a2a",
		client: server.Client(),
		a2aURL: server.URL,
	}

	a2aMsg := &api.A2AMessage{
		A2A:       true,
		AgentCard: true,
	}

	respBytes, err := stream.Forward(a2aMsg, "frompeerid123")
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	var a2aResp api.A2AResponse
	if err := json.Unmarshal(respBytes, &a2aResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !a2aResp.A2A {
		t.Error("expected A2A to be true")
	}
	if string(a2aResp.Response) != string(expectedCard) {
		t.Errorf("expected card %s, got %s", string(expectedCard), string(a2aResp.Response))
	}
	if a2aResp.Error != "" {
		t.Errorf("expected no error, got %s", a2aResp.Error)
	}
}
