package tcp

type Stream interface {
	// Forward processes data using parsed metadata and returns response bytes if successful.
	Forward(metadata any) ([]byte, error)
	// IsAllowed validates the data for this stream and returns true if allowed.
	IsAllowed(data []byte) bool
	// GetID returns the stream ID
	GetID() string
}
