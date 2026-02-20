package api

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestWriteAndReadLengthPrefixed(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	payload := []byte(`{"service":"weather"}`)
	done := make(chan error, 1)

	// Simulate the peer: read the framed message, echo back a response.
	go func() {
		defer server.Close()
		data, err := ReadLengthPrefixed(server)
		if err != nil {
			done <- err
			return
		}
		if string(data) != string(payload) {
			done <- nil // payload mismatch — fall through to response body check
			return
		}
		done <- WriteLengthPrefixed(server, []byte(`{"result":"ok"}`))
	}()

	if err := WriteLengthPrefixed(client, payload); err != nil {
		t.Fatalf("WriteLengthPrefixed failed: %v", err)
	}
	resp, err := ReadLengthPrefixed(client)
	if err != nil {
		t.Fatalf("ReadLengthPrefixed failed: %v", err)
	}
	if string(resp) != `{"result":"ok"}` {
		t.Fatalf("unexpected response: %s", resp)
	}
	if err := <-done; err != nil {
		t.Fatalf("peer goroutine error: %v", err)
	}
}

func TestReadLengthPrefixedOversizeRejected(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	// Write only the 4-byte length header encoding a value that exceeds MaxMessageSize.
	// This avoids allocating 16 MB of data in the test.
	go func() {
		defer server.Close()
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, MaxMessageSize+1)
		server.Write(lenBuf)
	}()

	_, err := ReadLengthPrefixed(client)
	if err == nil {
		t.Fatal("expected error for oversize message, got nil")
	}
}
