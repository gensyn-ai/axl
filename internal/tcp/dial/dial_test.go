package dial

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
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
	if !errors.Is(err, ErrInvalidPeerKey) {
		t.Errorf("expected ErrInvalidPeerKey, got %v", err)
	}
}

func TestDialPeerConnectionKeyTooShort(t *testing.T) {
	// Valid hex but only 16 bytes (should be 32)
	shortKey := strings.Repeat("ab", 16)
	_, err := DialPeerConnection(nil, 7000, shortKey, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for short key")
	}
	if !errors.Is(err, ErrInvalidPeerKey) {
		t.Errorf("expected ErrInvalidPeerKey, got %v", err)
	}
}

func TestDialPeerConnectionKeyTooLong(t *testing.T) {
	// Valid hex but 64 bytes (should be 32)
	longKey := strings.Repeat("ab", 64)
	_, err := DialPeerConnection(nil, 7000, longKey, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for long key")
	}
	if !errors.Is(err, ErrInvalidPeerKey) {
		t.Errorf("expected ErrInvalidPeerKey, got %v", err)
	}
}

func TestDialPeerConnectionEmptyKey(t *testing.T) {
	_, err := DialPeerConnection(nil, 7000, "", 30*time.Second)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !errors.Is(err, ErrInvalidPeerKey) {
		t.Errorf("expected ErrInvalidPeerKey, got %v", err)
	}
}

func TestDialPeerConnectionOddLengthHex(t *testing.T) {
	// Odd number of hex characters is invalid
	oddKey := strings.Repeat("a", 63) // 63 chars = invalid hex
	_, err := DialPeerConnection(nil, 7000, oddKey, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for odd-length hex")
	}
	if !errors.Is(err, ErrInvalidPeerKey) {
		t.Errorf("expected ErrInvalidPeerKey, got %v", err)
	}
}

func TestErrInvalidPeerKeyMessage(t *testing.T) {
	if ErrInvalidPeerKey.Error() != "invalid peer key" {
		t.Errorf("unexpected error message: %s", ErrInvalidPeerKey.Error())
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
			if !errors.Is(err, ErrInvalidPeerKey) {
				t.Errorf("expected ErrInvalidPeerKey, got %v", err)
			}
		})
	}
}

func TestDialPeerConnectionKeyLengthBoundaries(t *testing.T) {
	// Test various key lengths that should all fail validation
	// Note: 32 bytes is the valid length, but we can't test it without a real gVisor stack
	tests := []struct {
		name      string
		byteCount int
	}{
		{"0 bytes", 0},
		{"1 byte", 1},
		{"16 bytes", 16},
		{"31 bytes", 31},
		// 32 bytes is valid - skip since nil stack panics
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
			if !errors.Is(err, ErrInvalidPeerKey) {
				t.Errorf("expected ErrInvalidPeerKey, got %v", err)
			}
		})
	}
}

func TestValidKeyFormats(t *testing.T) {
	// These are valid hex formats that should pass validation
	// We only verify they decode to 32 bytes, since we can't dial without a stack
	validKeys := []string{
		strings.Repeat("00", 32),                    // All zeros
		strings.Repeat("ff", 32),                    // All ones
		strings.Repeat("ab", 32),                    // Repeating pattern
		"0123456789abcdef" + strings.Repeat("00", 24), // Mixed digits and letters
		strings.ToUpper(strings.Repeat("ab", 32)),  // Uppercase
		"AbCdEf" + strings.Repeat("00", 29),        // Mixed case
	}

	for _, key := range validKeys {
		decoded, err := hex.DecodeString(key)
		if err != nil {
			t.Errorf("key %q should be valid hex: %v", key[:16]+"...", err)
			continue
		}
		if len(decoded) != 32 {
			t.Errorf("key %q should decode to 32 bytes, got %d", key[:16]+"...", len(decoded))
		}
	}
}

func TestInvalidKeyFormats(t *testing.T) {
	// These should all fail hex validation
	invalidKeys := []string{
		"",                           // Empty
		"g" + strings.Repeat("0", 63), // Invalid hex char
		strings.Repeat("0", 63),      // Odd length
		" " + strings.Repeat("0", 63), // Leading space
		strings.Repeat("0", 63) + " ", // Trailing space
	}

	for _, key := range invalidKeys {
		_, err := hex.DecodeString(key)
		if err == nil {
			// If hex.DecodeString succeeds, check length
			decoded, _ := hex.DecodeString(key)
			if len(decoded) == 32 {
				t.Errorf("key should be invalid but passed: %q", key)
			}
		}
	}
}
