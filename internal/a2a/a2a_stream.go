package a2a

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gensyn-ai/axl/api"
)

type A2AStream struct {
	ID     string `json:"id"`
	client *http.Client
	a2aURL string
}

func NewA2AStream(a2aURL string) *A2AStream {
	return &A2AStream{ID: "a2a", client: &http.Client{Timeout: 30 * time.Second}, a2aURL: a2aURL}
}

func (a *A2AStream) GetID() string {
	return a.ID
}

func (a *A2AStream) IsAllowed(data []byte, a2aMsg any) bool {
	a2aMsgPtr, ok := a2aMsg.(*api.A2AMessage)
	if !ok {
		return false
	}
	if err := json.Unmarshal(data, a2aMsgPtr); err != nil {
		return false
	}
	return a2aMsgPtr.A2A
}

func (a *A2AStream) Forward(a2aMsg any, fromPeerId string) (respBytes []byte, err error) {
	a2aMsgPtr, ok := a2aMsg.(*api.A2AMessage)
	if !ok {
		return nil, nil
	}
	var respData json.RawMessage
	if a2aMsgPtr.AgentCard {
		respData, err = GetAgentCard(fromPeerId, a.client, a.a2aURL)
	} else {
		respData, err = ForwardToA2A(a2aMsgPtr.Request, fromPeerId, a.client, a.a2aURL)
	}

	var a2aResp api.A2AResponse
	a2aResp.A2A = true

	if err != nil {
		a2aResp.Error = err.Error()
	} else if respData != nil {
		a2aResp.Response = respData
	} else {
		return nil, nil
	}

	respBytes, err = json.Marshal(a2aResp)
	if err != nil {
		return nil, err
	}

	return respBytes, nil
}
