package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForwardToRouterSuccess(t *testing.T) {
	// Create a mock router server
	expectedService := "weather"
	expectedRequest := json.RawMessage(`{"method":"tools/call"}`)
	expectedFromPeerId := "abc123"
	expectedResponse := json.RawMessage(`{"result":"sunny"}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}

		var req RouterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.Service != expectedService {
			t.Errorf("expected service %s, got %s", expectedService, req.Service)
		}
		if req.FromPeerId != expectedFromPeerId {
			t.Errorf("expected from_peer_id %s, got %s", expectedFromPeerId, req.FromPeerId)
		}

		resp := RouterResponse{
			Response: expectedResponse,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	result, err := ForwardToRouter(expectedService, expectedRequest, expectedFromPeerId, server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ForwardToRouter failed: %v", err)
	}

	if string(result) != string(expectedResponse) {
		t.Errorf("expected response %s, got %s", string(expectedResponse), string(result))
	}
}

func TestForwardToRouterWithRouterError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := RouterResponse{
			Error: "service not found",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	_, err := ForwardToRouter("unknown", json.RawMessage(`{}`), "key", server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	expectedErr := "router error: service not found"
	if err.Error() != expectedErr {
		t.Errorf("expected error %q, got %q", expectedErr, err.Error())
	}
}

func TestForwardToRouterHTTPError(t *testing.T) {
	// Close the server immediately so any request gets "connection refused".
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	_, err := ForwardToRouter("weather", json.RawMessage(`{}`), "key", server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.HasPrefix(err.Error(), "failed to contact router") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestForwardToRouterInvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	_, err := ForwardToRouter("weather", json.RawMessage(`{}`), "key", server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	expectedPrefix := "failed to parse router response"
	if len(err.Error()) < len(expectedPrefix) || err.Error()[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("expected error to start with %q, got %q", expectedPrefix, err.Error())
	}
}

func TestForwardToRouterEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := RouterResponse{
			Response: nil, // Empty response
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	result, err := ForwardToRouter("weather", json.RawMessage(`{}`), "key", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("ForwardToRouter failed: %v", err)
	}

	// When Response is nil, JSON encodes as null, which unmarshals to []byte("null")
	if string(result) != "null" {
		t.Errorf("expected 'null' response, got %s", string(result))
	}
}

func TestRouterRequestStructure(t *testing.T) {
	req := RouterRequest{
		Service:    "weather",
		Request:    json.RawMessage(`{"method":"tools/call"}`),
		FromPeerId: "abc123",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded["service"] != "weather" {
		t.Errorf("expected service weather, got %v", decoded["service"])
	}
	if decoded["from_peer_id"] != "abc123" {
		t.Errorf("expected from_peer_id abc123, got %v", decoded["from_peer_id"])
	}
}

func TestRouterResponseStructure(t *testing.T) {
	tests := []struct {
		name     string
		resp     RouterResponse
		checkVal func(map[string]interface{}) error
	}{
		{
			name: "response only",
			resp: RouterResponse{
				Response: json.RawMessage(`{"result":"ok"}`),
			},
			checkVal: func(m map[string]interface{}) error {
				// RouterResponse always includes error field (no omitempty tag)
				if m["error"] != "" {
					return fmt.Errorf("expected empty error field, got %v", m["error"])
				}
				return nil
			},
		},
		{
			name: "error only",
			resp: RouterResponse{
				Error: "something went wrong",
			},
			checkVal: func(m map[string]interface{}) error {
				if m["error"] != "something went wrong" {
					return fmt.Errorf("expected error field, got %v", m["error"])
				}
				return nil
			},
		},
		{
			name: "both response and error",
			resp: RouterResponse{
				Response: json.RawMessage(`{"partial":"data"}`),
				Error:    "partial failure",
			},
			checkVal: func(m map[string]interface{}) error {
				if m["error"] != "partial failure" {
					return fmt.Errorf("expected error field, got %v", m["error"])
				}
				if m["response"] == nil {
					return fmt.Errorf("expected response field to be present")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.resp)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded map[string]interface{}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if err := tt.checkVal(decoded); err != nil {
				t.Error(err)
			}
		})
	}
}
