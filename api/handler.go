package api

import (
	"net/http"

	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// NewHandler creates the HTTP handler with all API routes configured.
// This is extracted from main to enable testing of HTTP routing.
func NewHandler(yggCore *core.Core, netStack *stack.Stack) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/topology", HandleTopology(yggCore))
	mux.HandleFunc("/send", HandleSend(netStack))
	mux.HandleFunc("/recv", HandleRecv)
	mux.HandleFunc("/mcp/", HandleMCP(netStack))
	mux.HandleFunc("/a2a/", HandleA2A(netStack))
	return mux
}
