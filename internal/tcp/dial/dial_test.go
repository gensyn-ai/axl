package dial

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// These tests focus on key validation since actual dialing requires a real gVisor stack.
// The DialPeerConnection function will panic if passed a nil stack (gVisor doesn't handle it),
// so we only test the key validation path here.

func TestDialPeerConnectionInvalidHex(t *testing.T) {
	// Invalid hex characters - should fail before attempting dial
	_, err := DialPeerConnection(nil, 7000, "not-valid-hex!", 30*time.Second)
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
	if !errors.Is(err, ErrInvalidPeerId) {
		t.Errorf("expected ErrInvalidPeerId, got %v", err)
	}
}

func TestDialPeerConnectionKeyTooShort(t *testing.T) {
	// Valid hex but only 16 bytes (should be 32)
	shortKey := strings.Repeat("ab", 16)
	_, err := DialPeerConnection(nil, 7000, shortKey, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for short key")
	}
	if !errors.Is(err, ErrInvalidPeerId) {
		t.Errorf("expected ErrInvalidPeerId, got %v", err)
	}
}

func TestDialPeerConnectionKeyTooLong(t *testing.T) {
	// Valid hex but 64 bytes (should be 32)
	longKey := strings.Repeat("ab", 64)
	_, err := DialPeerConnection(nil, 7000, longKey, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for long key")
	}
	if !errors.Is(err, ErrInvalidPeerId) {
		t.Errorf("expected ErrInvalidPeerId, got %v", err)
	}
}

func TestDialPeerConnectionEmptyKey(t *testing.T) {
	_, err := DialPeerConnection(nil, 7000, "", 30*time.Second)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !errors.Is(err, ErrInvalidPeerId) {
		t.Errorf("expected ErrInvalidPeerId, got %v", err)
	}
}

func TestDialPeerConnectionOddLengthHex(t *testing.T) {
	// Odd number of hex characters is invalid
	oddKey := strings.Repeat("a", 63) // 63 chars = invalid hex
	_, err := DialPeerConnection(nil, 7000, oddKey, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for odd-length hex")
	}
	if !errors.Is(err, ErrInvalidPeerId) {
		t.Errorf("expected ErrInvalidPeerId, got %v", err)
	}
}

func TestErrInvalidPeerIdMessage(t *testing.T) {
	if ErrInvalidPeerId.Error() != "invalid peer ID" {
		t.Errorf("unexpected error message: %s", ErrInvalidPeerId.Error())
	}
}

func TestErrDialPeerMessage(t *testing.T) {
	if ErrDialPeer.Error() != "failed to reach peer" {
		t.Errorf("unexpected error message: %s", ErrDialPeer.Error())
	}
}

// Test various invalid hex patterns
func TestDialPeerConnectionInvalidHexPatterns(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"spaces", "ab cd ef " + strings.Repeat("00", 29)},
		{"special chars", "ab!@#$" + strings.Repeat("00", 29)},
		{"unicode", "ab\u00ff" + strings.Repeat("00", 30)},
		{"newline", "ab\n" + strings.Repeat("00", 30)},
		{"tab", "ab\t" + strings.Repeat("00", 30)},
		{"null byte", "ab\x00" + strings.Repeat("00", 30)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DialPeerConnection(nil, 7000, tt.key, 30*time.Second)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrInvalidPeerId) {
				t.Errorf("expected ErrInvalidPeerId, got %v", err)
			}
		})
	}
}

func TestDialPeerConnectionKeyLengthBoundaries(t *testing.T) {
	// Test various key lengths that should all fail validation (32 bytes is valid)
	tests := []struct {
		name      string
		byteCount int
	}{
		{"0 bytes", 0},
		{"1 byte", 1},
		{"16 bytes", 16},
		{"31 bytes", 31},
		// 32 bytes is valid — tested separately via dialTCP injection
		{"33 bytes", 33},
		{"64 bytes", 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := strings.Repeat("ab", tt.byteCount)
			_, err := DialPeerConnection(nil, 7000, key, 30*time.Second)

			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrInvalidPeerId) {
				t.Errorf("expected ErrInvalidPeerId, got %v", err)
			}
		})
	}
}

// setDialTCP replaces dialTCP for the duration of a test and restores it on cleanup.
func setDialTCP(t *testing.T, fn func(*stack.Stack, tcpip.FullAddress) (net.Conn, error)) {
	t.Helper()
	old := dialTCP
	dialTCP = fn
	t.Cleanup(func() { dialTCP = old })
}

func TestDialPeerConnectionDialError(t *testing.T) {
	setDialTCP(t, func(_ *stack.Stack, _ tcpip.FullAddress) (net.Conn, error) {
		return nil, errors.New("connection refused")
	})

	validKey := strings.Repeat("ab", 32)
	_, err := DialPeerConnection(nil, 7000, validKey, 30*time.Second)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrDialPeer) {
		t.Errorf("expected ErrDialPeer, got %v", err)
	}
}

func TestDialPeerConnectionSuccess(t *testing.T) {
	peerConn, localConn := net.Pipe()
	defer peerConn.Close()
	defer localConn.Close()

	setDialTCP(t, func(_ *stack.Stack, _ tcpip.FullAddress) (net.Conn, error) {
		return peerConn, nil
	})

	validKey := strings.Repeat("ab", 32)
	conn, err := DialPeerConnection(nil, 7000, validKey, 5*time.Second)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil connection")
	}
	conn.Close()
}

