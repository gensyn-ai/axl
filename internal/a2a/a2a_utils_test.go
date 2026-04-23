package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForwardToA2ASuccess(t *testing.T) {
	expectedRequest := json.RawMessage(`{"jsonrpc":"2.0","method":"message/send","id":1}`)
	expectedResponse := json.RawMessage(`{"jsonrpc":"2.0","result":{"id":"task-123"},"id":1}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("A2A-Version") != "1.0" {
			t.Errorf("expected A2A-Version 1.0, got %s", r.Header.Get("A2A-Version"))
		}
		if r.Header.Get("X-From-Peer-Id") != "peer-abc123" {
			t.Errorf("expected X-From-Peer-Id peer-abc123, got %s", r.Header.Get("X-From-Peer-Id"))
		}

		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req["method"] != "message/send" {
			t.Errorf("expected method message/send, got %v", req["method"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(expectedResponse)
	}))
	defer server.Close()

	result, err := ForwardToA2A(expectedRequest, "peer-abc123", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ForwardToA2A failed: %v", err)
	}

	if string(result) != string(expectedResponse) {
		t.Errorf("expected response %s, got %s", string(expectedResponse), string(result))
	}
}

func TestForwardToA2ANoFromPeerId(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-From-Peer-Id") != "" {
			t.Errorf("expected no X-From-Peer-Id header, got %s", r.Header.Get("X-From-Peer-Id"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	}))
	defer server.Close()

	_, err := ForwardToA2A(json.RawMessage(`{}`), "", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ForwardToA2A failed: %v", err)
	}
}

func TestForwardToA2AConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	_, err := ForwardToA2A(json.RawMessage(`{}`), "peer-abc", server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.HasPrefix(err.Error(), "failed to contact a2a server") {
		t.Errorf("expected error to start with %q, got %q", "failed to contact a2a server", err.Error())
	}
}

func TestForwardToA2AErrorResponse(t *testing.T) {
	errorResponse := json.RawMessage(`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":1}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(errorResponse)
	}))
	defer server.Close()

	result, err := ForwardToA2A(json.RawMessage(`{}`), "peer-abc", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ForwardToA2A failed: %v", err)
	}

	// A2A server error responses are returned as-is (they are valid JSON-RPC responses)
	if string(result) != string(errorResponse) {
		t.Errorf("expected response %s, got %s", string(errorResponse), string(result))
	}
}

func TestGetAgentCardSuccess(t *testing.T) {
	expectedCard := json.RawMessage(`{"name":"test-agent","url":"http://example.com","version":"1.0"}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/.well-known/agent-card.json" {
			t.Errorf("expected path /.well-known/agent-card.json, got %s", r.URL.Path)
		}
		if r.Header.Get("X-From-Peer-Id") != "peer-xyz" {
			t.Errorf("expected X-From-Peer-Id peer-xyz, got %s", r.Header.Get("X-From-Peer-Id"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(expectedCard)
	}))
	defer server.Close()

	result, err := GetAgentCard("peer-xyz", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("GetAgentCard failed: %v", err)
	}
	if string(result) != string(expectedCard) {
		t.Errorf("expected card %s, got %s", string(expectedCard), string(result))
	}
}

func TestGetAgentCardNoFromPeerId(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-From-Peer-Id") != "" {
			t.Errorf("expected no X-From-Peer-Id header, got %s", r.Header.Get("X-From-Peer-Id"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"agent"}`))
	}))
	defer server.Close()

	_, err := GetAgentCard("", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("GetAgentCard failed: %v", err)
	}
}

func TestGetAgentCardConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	_, err := GetAgentCard("peer-abc", server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "failed to contact a2a server") {
		t.Errorf("expected error to start with %q, got %q", "failed to contact a2a server", err.Error())
	}
}
