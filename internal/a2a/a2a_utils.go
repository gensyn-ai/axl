package a2a

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ForwardToA2A forwards a raw A2A JSON-RPC request to the A2A server
func ForwardToA2A(
	request json.RawMessage,
	client *http.Client,
	a2aURL string,
) (json.RawMessage, error) {
	// Send raw JSON-RPC request directly to the A2A server
	resp, err := client.Post(a2aURL, "application/json", bytes.NewReader(request))
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
