package mcp

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gensyn-ai/axl/api"
)

type MCPStream struct {
	ID        string `json:"id"`
	client    *http.Client
	routerURL string
}

func NewMCPStream(routerURL string) *MCPStream {
	return &MCPStream{ID: "mcp", client: &http.Client{Timeout: 30 * time.Second}, routerURL: routerURL}
}

func (m *MCPStream) GetID() string {
	return m.ID
}

func (m *MCPStream) IsAllowed(data []byte, mcpMsg any) bool {
	mcpMsgPtr, ok := mcpMsg.(*api.MCPMessage)
	if !ok {
		return false
	}
	if err := json.Unmarshal(data, mcpMsgPtr); err == nil && mcpMsgPtr.Service != "" {
		return true
	}
	return false
}

func (m *MCPStream) Forward(mcpMsg any, fromPeerId string) (respBytes []byte, err error) {
	mcpMsgPtr, ok := mcpMsg.(*api.MCPMessage)
	if !ok {
		return nil, nil
	}
	respData, err := ForwardToRouter(mcpMsgPtr.Service, mcpMsgPtr.Request, fromPeerId, m.client, m.routerURL)

	var mcpResp api.MCPResponse
	mcpResp.Service = mcpMsgPtr.Service

	if err != nil {
		mcpResp.Error = err.Error()
	} else if respData != nil {
		mcpResp.Response = respData
	} else { // no response supplied by router
		return nil, nil
	}

	respBytes, err = json.Marshal(mcpResp)
	if err != nil {
		return nil, err
	}

	return respBytes, nil
}
