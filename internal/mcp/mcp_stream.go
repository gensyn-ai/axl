package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"example.com/api"
)

type MCPStream struct {
	ID        string `json:"id"`
	client    *http.Client
	routerURL string
}

func NewMCPStream() *MCPStream {
	return &MCPStream{ID: "mcp", client: &http.Client{}, routerURL: "http://localhost:8080"}
}

func (m *MCPStream) GetID() string {
	return m.ID
}

func (m *MCPStream) IsAllowed(data []byte) (*api.MCPMessage, bool) {
	var mcpMsg api.MCPMessage
	if err := json.Unmarshal(data, &mcpMsg); err == nil && mcpMsg.Service != "" {
		return &mcpMsg, true
	}
	return nil, false
}

func (m *MCPStream) Forward(mcpMsg *api.MCPMessage) (respBytes []byte, err error) {
	respData, err := ForwardToRouter(mcpMsg.Service, mcpMsg.Request, mcpMsg.FromKey, m.client, m.routerURL)

	var mcpResp api.MCPResponse
	mcpResp.Service = mcpMsg.Service

	if err != nil {
		log.Printf("MCP forward error: %v", err)
		mcpResp.Error = err.Error()
	} else if respData != nil {
		mcpResp.Response = respData
	} else { // If forward succeeds, we require a response
		return nil, fmt.Errorf("no response data from router, service: %s", mcpMsg.Service)
	}

	respBytes, err = json.Marshal(mcpResp)
	if err != nil {
		return nil, err
	}

	return respBytes, nil
}
