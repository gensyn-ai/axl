package listen

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gensyn-ai/axl/api"
	"github.com/gensyn-ai/axl/internal/mcp"
)

// --- sendResponse tests ---

func TestSendResponseSuccess(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	testData := []byte("hello world")
	done := make(chan error, 1)

	// Read from server side
	go func() {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(server, lenBuf); err != nil {
			done <- err
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)
		if length != uint32(len(testData)) {
			done <- errors.New("length mismatch")
			return
		}

		dataBuf := make([]byte, length)
		if _, err := io.ReadFull(server, dataBuf); err != nil {
			done <- err
			return
		}
		if string(dataBuf) != string(testData) {
			done <- errors.New("data mismatch")
			return
		}
		done <- nil
	}()

	// Send from client side
	err := sendResponse(client, testData)
	if err != nil {
		t.Fatalf("sendResponse failed: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server read failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for server")
	}
}

func TestSendResponseSmallData(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// Test with minimal 1-byte data (empty data writes can block on net.Pipe)
	testData := []byte{0x42}
	done := make(chan error, 1)

	go func() {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(server, lenBuf); err != nil {
			done <- err
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)
		if length != 1 {
			done <- errors.New("expected length 1")
			return
		}
		dataBuf := make([]byte, 1)
		if _, err := io.ReadFull(server, dataBuf); err != nil {
			done <- err
			return
		}
		if dataBuf[0] != 0x42 {
			done <- errors.New("data mismatch")
			return
		}
		done <- nil
	}()

	err := sendResponse(client, testData)
	if err != nil {
		t.Fatalf("sendResponse failed: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server read failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestSendResponseLargeData(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// 64KB of data
	testData := make([]byte, 64*1024)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	done := make(chan error, 1)

	go func() {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(server, lenBuf); err != nil {
			done <- err
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)

		dataBuf := make([]byte, length)
		if _, err := io.ReadFull(server, dataBuf); err != nil {
			done <- err
			return
		}

		// Verify data integrity
		for i, b := range dataBuf {
			if b != byte(i%256) {
				done <- errors.New("data corruption")
				return
			}
		}
		done <- nil
	}()

	err := sendResponse(client, testData)
	if err != nil {
		t.Fatalf("sendResponse failed: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server read failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

// errWriteFailed is the sentinel error returned by mockWriteConn on a simulated write failure.
var errWriteFailed = errors.New("write failed")

// mockWriteConn is a minimal net.Conn implementation for testing write errors.
type mockWriteConn struct {
	writeCount int
	failOnCall int // 0 = never fail, 1 = fail on first call, 2 = fail on second call
}

func (m *mockWriteConn) Read(b []byte) (int, error)         { return 0, io.EOF }
func (m *mockWriteConn) Close() error                       { return nil }
func (m *mockWriteConn) LocalAddr() net.Addr                { return nil }
func (m *mockWriteConn) RemoteAddr() net.Addr               { return nil }
func (m *mockWriteConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockWriteConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockWriteConn) SetWriteDeadline(t time.Time) error { return nil }
func (m *mockWriteConn) Write(b []byte) (int, error) {
	m.writeCount++
	if m.failOnCall > 0 && m.writeCount == m.failOnCall {
		return 0, errWriteFailed
	}
	return len(b), nil
}

func TestSendResponseWriteLengthError(t *testing.T) {
	conn := &mockWriteConn{failOnCall: 1} // Fail on first write (length)

	err := sendResponse(conn, []byte("test"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errWriteFailed) {
		t.Errorf("expected errWriteFailed in chain, got: %v", err)
	}
	if !strings.Contains(err.Error(), "length") {
		t.Errorf("expected 'length' in error message, got: %v", err)
	}
}

func TestSendResponseWriteDataError(t *testing.T) {
	conn := &mockWriteConn{failOnCall: 2} // Fail on second write (data)

	err := sendResponse(conn, []byte("test"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errWriteFailed) {
		t.Errorf("expected errWriteFailed in chain, got: %v", err)
	}
	if !strings.Contains(err.Error(), "data") {
		t.Errorf("expected 'data' in error message, got: %v", err)
	}
}

// --- frameMessage helper for tests ---
func frameMessage(data []byte) []byte {
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	return append(lenBuf, data...)
}

// --- handleTCPConn tests ---
// These tests are more integration-style since handleTCPConn has many dependencies

// mockTCPAddr returns an address that will produce a valid fromKey
type mockTCPAddr struct {
	ip   string
	port int
}

func (a *mockTCPAddr) Network() string { return "tcp" }
func (a *mockTCPAddr) String() string {
	return net.JoinHostPort(a.ip, string(rune('0'+a.port%10)))
}

// testConn wraps net.Conn to provide a custom RemoteAddr
type testConn struct {
	net.Conn
	remoteAddr net.Addr
}

func (t *testConn) RemoteAddr() net.Addr {
	if t.remoteAddr != nil {
		return t.remoteAddr
	}
	return t.Conn.RemoteAddr()
}

func TestHandleTCPConnNonMCPMessage(t *testing.T) {
	t.Cleanup(func() { api.DefaultRecvQueue.Reset() })
	api.DefaultRecvQueue.Reset()

	client, server := net.Pipe()

	// Wrap server to provide a valid Yggdrasil-like address
	// Address 0200::1 starts with 02, which Yggdrasil uses for valid addresses
	wrappedServer := &testConn{
		Conn:       server,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	// Non-MCP message (doesn't have "service" field)
	nonMCPData := []byte(`{"type":"custom","payload":"data"}`)

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConn(wrappedServer, NewMultiplexer())
	}()

	// Write message and close to trigger EOF
	client.Write(frameMessage(nonMCPData))
	client.Close()

	// Wait for handler to finish
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handleTCPConn to finish")
	}

	// Check that message was added to DefaultRecvQueue
	if api.DefaultRecvQueue.Len() != 1 {
		t.Fatalf("expected 1 message in queue, got %d", api.DefaultRecvQueue.Len())
	}

	snap := api.DefaultRecvQueue.Snapshot()
	if string(snap[0].Data) != string(nonMCPData) {
		t.Errorf("expected data %s, got %s", string(nonMCPData), string(snap[0].Data))
	}
}

func TestHandleTCPConnMultipleMessages(t *testing.T) {
	t.Cleanup(func() { api.DefaultRecvQueue.Reset() })
	api.DefaultRecvQueue.Reset()

	client, server := net.Pipe()
	wrappedServer := &testConn{
		Conn:       server,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	messages := []string{
		`{"msg":1}`,
		`{"msg":2}`,
		`{"msg":3}`,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConn(wrappedServer, NewMultiplexer())
	}()

	// Send all messages then close
	for _, msg := range messages {
		client.Write(frameMessage([]byte(msg)))
	}
	client.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	if api.DefaultRecvQueue.Len() != 3 {
		t.Fatalf("expected 3 messages in queue, got %d", api.DefaultRecvQueue.Len())
	}

	snap := api.DefaultRecvQueue.Snapshot()
	for i, msg := range messages {
		if string(snap[i].Data) != msg {
			t.Errorf("message %d: expected %s, got %s", i, msg, string(snap[i].Data))
		}
	}
}

func TestHandleTCPConnMCPMessageWithResponse(t *testing.T) {
	// Create a mock MCP router server
	expectedResponse := json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	routerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mcp.RouterResponse{
			Response: expectedResponse,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer routerServer.Close()

	client, server := net.Pipe()
	wrappedServer := &testConn{
		Conn:       server,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	// Prepare MCP message
	mcpMsg := api.MCPMessage{
		Service: "weather",
		Request: json.RawMessage(`{"jsonrpc":"2.0","method":"tools/list","id":1}`),
	}
	mcpData, _ := json.Marshal(mcpMsg)

	// responseData is written by the reader goroutine and read by the test after sync.
	responseData := make(chan []byte, 1)

	// Read the length-prefixed response sent back by handleTCPConn.
	go func() {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(client, lenBuf); err != nil {
			responseData <- nil
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)
		buf := make([]byte, length)
		if _, err := io.ReadFull(client, buf); err != nil {
			responseData <- nil
			return
		}
		responseData <- buf
	}()

	mux := NewMultiplexer()
	mcpStream := mcp.NewMCPStream(routerServer.URL)
	mux.AddSource(mcpStream, func() any { return &api.MCPMessage{} })

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		handleTCPConn(wrappedServer, mux)
	}()

	// Send the MCP message; handleTCPConn will forward it to the router and write back a response.
	client.Write(frameMessage(mcpData))

	// Block until we have the response — no sleep, no skip.
	select {
	case data := <-responseData:
		if data == nil {
			t.Fatal("failed to read response from handleTCPConn")
		}
		var mcpResp api.MCPResponse
		if err := json.Unmarshal(data, &mcpResp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if mcpResp.Service != "weather" {
			t.Errorf("expected service 'weather', got %s", mcpResp.Service)
		}
		if string(mcpResp.Response) != string(expectedResponse) {
			t.Errorf("expected response %s, got %s", string(expectedResponse), string(mcpResp.Response))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for response from handleTCPConn")
	}

	// Close client to let the handler's next ReadFull return EOF and exit cleanly.
	client.Close()

	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handler to exit")
	}
}

func TestHandleTCPConnQueueOverflow(t *testing.T) {
	t.Cleanup(func() { api.DefaultRecvQueue.Reset() })
	api.DefaultRecvQueue.Reset()

	client, server := net.Pipe()
	wrappedServer := &testConn{
		Conn:       server,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConn(wrappedServer, NewMultiplexer())
	}()

	// Send more than 100 messages to test queue overflow
	for i := 0; i < 105; i++ {
		msg := []byte(fmt.Sprintf(`{"index":%d}`, i))
		client.Write(frameMessage(msg))
	}
	client.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// Queue should be capped at 100
	if api.DefaultRecvQueue.Len() > 100 {
		t.Errorf("expected queue length <= 100, got %d", api.DefaultRecvQueue.Len())
	}
}

func TestHandleTCPConnImmediateEOF(t *testing.T) {
	client, server := net.Pipe()
	wrappedServer := &testConn{
		Conn:       server,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConn(wrappedServer, NewMultiplexer())
	}()

	// Close immediately
	client.Close()

	select {
	case <-done:
		// Success - handler returned
	case <-time.After(2 * time.Second):
		t.Fatal("timeout - handler did not return on EOF")
	}
}

func TestHandleTCPConnPartialLengthRead(t *testing.T) {
	client, server := net.Pipe()
	wrappedServer := &testConn{
		Conn:       server,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConn(wrappedServer, NewMultiplexer())
	}()

	// Write only 2 bytes of the 4-byte length header, then close
	client.Write([]byte{0x00, 0x00})
	client.Close()

	select {
	case <-done:
		// Success - handler returned on read error
	case <-time.After(2 * time.Second):
		t.Fatal("timeout - handler did not return on partial read")
	}
}

func TestHandleTCPConnPartialDataRead(t *testing.T) {
	client, server := net.Pipe()
	wrappedServer := &testConn{
		Conn:       server,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConn(wrappedServer, NewMultiplexer())
	}()

	// Write length header saying 100 bytes, but only send 10
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, 100)
	client.Write(lenBuf)
	client.Write([]byte("only10byte"))
	client.Close()

	select {
	case <-done:
		// Success - handler returned on read error
	case <-time.After(2 * time.Second):
		t.Fatal("timeout - handler did not return on partial data read")
	}
}

// --- peerIDFromAddr tests ---

func TestPeerIDFromAddrValidIPv6(t *testing.T) {
	addr := &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345}
	result := peerIDFromAddr(addr)
	if len(result) != 64 {
		t.Errorf("expected 64-char hex string, got %d chars: %q", len(result), result)
	}
}

func TestPeerIDFromAddrNilIP(t *testing.T) {
	addr := &net.TCPAddr{IP: nil, Port: 0}
	result := peerIDFromAddr(addr)
	if result != "" {
		t.Errorf("expected empty string for nil IP, got %q", result)
	}
}

func TestPeerIDFromAddrDifferentAddressesProduceDifferentIDs(t *testing.T) {
	addr1 := &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345}
	addr2 := &net.TCPAddr{IP: net.ParseIP("201::1"), Port: 12345}
	id1 := peerIDFromAddr(addr1)
	id2 := peerIDFromAddr(addr2)
	if id1 == id2 {
		t.Errorf("expected different peer IDs for different addresses, both got %q", id1)
	}
}

func TestPeerIDFromAddrSameAddressProducesSameID(t *testing.T) {
	addr := &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345}
	id1 := peerIDFromAddr(addr)
	id2 := peerIDFromAddr(addr)
	if id1 != id2 {
		t.Errorf("expected same peer ID for same address, got %q and %q", id1, id2)
	}
}

// --- handleTCPConn branch coverage ---

// writeFailingConn proxies reads to the underlying net.Conn but rejects all writes.
// Used to exercise sendResponse error paths without timing-sensitive connection teardown.
type writeFailingConn struct {
	net.Conn
}

func (c *writeFailingConn) Write([]byte) (int, error) {
	return 0, errors.New("write disabled")
}

func TestHandleTCPConnOversizedMessage(t *testing.T) {
	client, server := net.Pipe()
	wrappedServer := &testConn{
		Conn:       server,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConn(wrappedServer, NewMultiplexer())
	}()

	// Write a length header encoding MaxMessageSize+1 — handler must drop the connection.
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, api.MaxMessageSize+1)
	client.Write(lenBuf)

	select {
	case <-done:
		// Expected: handler returned without reading a payload.
	case <-time.After(2 * time.Second):
		t.Fatal("timeout — handler did not return on oversized message")
	}
	client.Close()
}

func TestHandleTCPConnStreamForwardError(t *testing.T) {
	client, server := net.Pipe()
	wrappedServer := &testConn{
		Conn:       server,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	testData := []byte(`{"type":"forward-error-test"}`)
	errStream := &mockStream{
		id:          "err-stream",
		allowedData: testData,
		forwardErr:  errors.New("upstream unavailable"),
	}
	mux := NewMultiplexer()
	mux.AddSource(errStream, func() any { return &struct{}{} })

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConn(wrappedServer, mux)
	}()

	// net.Pipe is synchronous: Write blocks until server's ReadFull consumes all bytes.
	// After it returns, the server has already called Forward (which returns the error)
	// and is looping back to read the next message.
	client.Write(frameMessage(testData))
	client.Close() // EOF on next read causes handler to exit cleanly.

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHandleTCPConnSendResponseFailure(t *testing.T) {
	client, server := net.Pipe()
	// writeFailingConn proxies reads from server but blocks all writes,
	// so sendResponse inside handleTCPConn will always fail.
	wrappedServer := &testConn{
		Conn:       &writeFailingConn{server},
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("200::1"), Port: 12345},
	}

	testData := []byte(`{"type":"send-response-test"}`)
	respondingStream := &mockStream{
		id:            "responder",
		allowedData:   testData,
		forwardResult: []byte(`{"result":"ok"}`),
	}
	mux := NewMultiplexer()
	mux.AddSource(respondingStream, func() any { return &struct{}{} })

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConn(wrappedServer, mux)
	}()

	// Write blocks until server's ReadFull consumes the bytes; sendResponse then fails
	// immediately because writeFailingConn rejects the write unconditionally.
	client.Write(frameMessage(testData))
	client.Close() // EOF on next read causes handler to exit cleanly.

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}
