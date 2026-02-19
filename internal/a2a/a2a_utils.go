package a2a

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetAgentCard fetches the agent card from the A2A server's well-known endpoint.
func GetAgentCard(fromPeerId string, client *http.Client, a2aURL string) (json.RawMessage, error) {
	req, err := http.NewRequest("GET", a2aURL+"/.well-known/agent.json", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if fromPeerId != "" {
		req.Header.Set("X-From-Peer-Id", fromPeerId)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to contact a2a server: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent card: %w", err)
	}

	return json.RawMessage(respBody), nil
}

// ForwardToA2A forwards a raw A2A JSON-RPC request to the A2A server
func ForwardToA2A(
	request json.RawMessage,
	fromPeerId string,
	client *http.Client,
	a2aURL string,
) (json.RawMessage, error) {
	// Send raw JSON-RPC request directly to the A2A server
	req, err := http.NewRequest("POST", a2aURL, bytes.NewReader(request))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if fromPeerId != "" {
		req.Header.Set("X-From-Peer-Id", fromPeerId)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to contact a2a server: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read a2a response: %w", err)
	}

	return json.RawMessage(respBody), nil
}
