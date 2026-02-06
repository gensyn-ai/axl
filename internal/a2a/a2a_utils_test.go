package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	result, err := ForwardToA2A(expectedRequest, server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ForwardToA2A failed: %v", err)
	}

	if string(result) != string(expectedResponse) {
		t.Errorf("expected response %s, got %s", string(expectedResponse), string(result))
	}
}

func TestForwardToA2AConnectionFailure(t *testing.T) {
	_, err := ForwardToA2A(json.RawMessage(`{}`), http.DefaultClient, "http://localhost:1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	expectedPrefix := "failed to contact a2a server"
	if len(err.Error()) < len(expectedPrefix) || err.Error()[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("expected error to start with %q, got %q", expectedPrefix, err.Error())
	}
}

func TestForwardToA2AErrorResponse(t *testing.T) {
	errorResponse := json.RawMessage(`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":1}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(errorResponse)
	}))
	defer server.Close()

	result, err := ForwardToA2A(json.RawMessage(`{}`), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ForwardToA2A failed: %v", err)
	}

	// A2A server error responses are returned as-is (they are valid JSON-RPC responses)
	if string(result) != string(errorResponse) {
		t.Errorf("expected response %s, got %s", string(errorResponse), string(result))
	}
}
