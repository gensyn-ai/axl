package api

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// MaxMessageSize is the maximum allowed size for a length-prefixed message.
// Defaults to 16 MB but can be overridden via node-config.json.
var MaxMessageSize uint32 = 16 * 1024 * 1024

// WriteLengthPrefixed sends a length-prefixed message over a TCP connection.
func WriteLengthPrefixed(conn net.Conn, data []byte) error {
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := conn.Write(lenBuf); err != nil {
		return fmt.Errorf("failed to send to peer: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to send to peer: %w", err)
	}
	return nil
}

// ReadLengthPrefixed reads a length-prefixed message from a TCP connection.
func ReadLengthPrefixed(conn net.Conn) ([]byte, error) {
	respLenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, respLenBuf); err != nil {
		return nil, fmt.Errorf("no response from peer: %w", err)
	}
	respLen := binary.BigEndian.Uint32(respLenBuf)
	if respLen > MaxMessageSize {
		return nil, fmt.Errorf("message size %d exceeds maximum %d", respLen, MaxMessageSize)
	}
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		return nil, fmt.Errorf("failed to read peer response: %w", err)
	}
	return respBuf, nil
}
