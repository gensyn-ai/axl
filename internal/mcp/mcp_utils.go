package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// RouterRequest is sent to the MCP router
type RouterRequest struct {
	Service    string          `json:"service"`
	Request    json.RawMessage `json:"request"`
	FromPeerId string          `json:"from_peer_id"`
}

// RouterResponse is returned by the MCP router
type RouterResponse struct {
	Response json.RawMessage `json:"response"`
	Error    string          `json:"error"`
}

// ForwardToRouter forwards an MCP request to the router service
func ForwardToRouter(
	service string,
	request json.RawMessage,
	fromPeerId string,
	client *http.Client,
	routerURL string,
) (json.RawMessage, error) {
	// Build router request
	routerReq := RouterRequest{
		Service:    service,
		Request:    request,
		FromPeerId: fromPeerId,
	}

	reqBody, err := json.Marshal(routerReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send to router
	resp, err := client.Post(routerURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to contact router: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read router response: %w", err)
	}

	// Parse router response
	var routerResp RouterResponse
	if err := json.Unmarshal(respBody, &routerResp); err != nil {
		return nil, fmt.Errorf("failed to parse router response: %w", err)
	}

	// Check for router-level error
	if routerResp.Error != "" {
		return nil, fmt.Errorf("router error: %s", routerResp.Error)
	}

	return routerResp.Response, nil
}
