package listen

type Stream interface {
	// GetID returns the stream ID
	GetID() string
	// IsAllowed validates the data for this stream and returns true if allowed.
	IsAllowed(data []byte, metadata any) bool
	// Forward processes data using parsed metadata and returns response bytes if successful.
	Forward(metadata any, fromPeerId string) ([]byte, error)
}
