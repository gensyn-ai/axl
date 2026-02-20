package api

import (
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type stubConn struct {
	writes    [][]byte
	writeErrs []error
	closed    bool
}

func (s *stubConn) Write(p []byte) (int, error) {
	var err error
	if len(s.writeErrs) > 0 {
		err = s.writeErrs[0]
		s.writeErrs = s.writeErrs[1:]
		if err != nil {
			return 0, err
		}
	}
	buf := make([]byte, len(p))
	copy(buf, p)
	s.writes = append(s.writes, buf)
	return len(p), nil
}

func (s *stubConn) Close() error {
	s.closed = true
	return nil
}

func resetDialer(t *testing.T) {
	t.Helper()
	original := dialPeerConnection
	t.Cleanup(func() {
		dialPeerConnection = original
	})
}

func setDialer(t *testing.T, conn peerConn, err error) *stubConn {
	t.Helper()
	resetDialer(t)
	stub := conn
	dialPeerConnection = func(_ *stack.Stack, _ int, _ string) (peerConn, error) {
		return stub, err
	}
	if c, ok := conn.(*stubConn); ok {
		return c
	}
	return nil
}

func validKeyHex() string {
	return strings.Repeat("ab", 32)
}

func TestHandleSendMethodNotAllowed(t *testing.T) {
	handler := HandleSend(7000, nil)
	req := httptest.NewRequest(http.MethodGet, "/send", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Result().StatusCode)
	}
}

func TestHandleSendMissingDestinationHeader(t *testing.T) {
	handler := HandleSend(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader("hello"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleSendInvalidDestinationHex(t *testing.T) {
	handler := HandleSend(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader("hello"))
	req.Header.Set("X-Destination-Peer-Id", "zzzz")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleSendInvalidKeyLength(t *testing.T) {
	handler := HandleSend(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader("hello"))
	req.Header.Set("X-Destination-Peer-Id", "abcd")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

type failingReader struct{}

func (f failingReader) Read(_ []byte) (int, error) {
	return 0, errors.New("boom")
}

func TestHandleSendBodyReadError(t *testing.T) {
	handler := HandleSend(7000, nil)
	req := httptest.NewRequest(http.MethodPost, "/send", failingReader{})
	req.Header.Set("X-Destination-Peer-Id", validKeyHex())
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleSendDialError(t *testing.T) {
	handler := HandleSend(7000, nil)
	setDialer(t, nil, errors.New("dial failed"))

	req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader("hello"))
	req.Header.Set("X-Destination-Peer-Id", validKeyHex())
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Result().StatusCode)
	}
}

func TestHandleSendWriteLengthError(t *testing.T) {
	handler := HandleSend(7000, nil)
	conn := &stubConn{writeErrs: []error{errors.New("len write failed")}}
	setDialer(t, conn, nil)

	req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader("hello"))
	req.Header.Set("X-Destination-Peer-Id", validKeyHex())
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Result().StatusCode)
	}
}

func TestHandleSendWriteDataError(t *testing.T) {
	handler := HandleSend(7000, nil)
	conn := &stubConn{writeErrs: []error{nil, errors.New("data write failed")}}
	setDialer(t, conn, nil)

	req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader("hello"))
	req.Header.Set("X-Destination-Peer-Id", validKeyHex())
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Result().StatusCode)
	}
}

func TestHandleSendSuccess(t *testing.T) {
	handler := HandleSend(7000, nil)
	conn := &stubConn{}
	setDialer(t, conn, nil)

	body := []byte("payload")
	req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(string(body)))
	req.Header.Set("X-Destination-Peer-Id", validKeyHex())
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Sent-Bytes"); got != "7" {
		t.Fatalf("expected X-Sent-Bytes 7, got %s", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("unexpected content type: %s", got)
	}
	if !conn.closed {
		t.Fatalf("expected connection to be closed")
	}
	if len(conn.writes) != 2 {
		t.Fatalf("expected 2 writes, got %d", len(conn.writes))
	}
	if got := conn.writes[0]; len(got) != 4 {
		t.Fatalf("expected length prefix of 4 bytes, got %d", len(got))
	}
	if encodedLen := binary.BigEndian.Uint32(conn.writes[0]); encodedLen != uint32(len(body)) {
		t.Fatalf("expected length prefix to encode %d, got %d", len(body), encodedLen)
	}
	if string(conn.writes[1]) != string(body) {
		t.Fatalf("expected payload %q, got %q", string(body), string(conn.writes[1]))
	}
}
