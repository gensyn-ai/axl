package tcp

type Stream interface {
	// Forward processes data using parsed metadata and returns response bytes if successful.
	Forward(metadata any) ([]byte, error)
	// IsAllowed validates the data for this stream and returns parsed metadata when applicable.
	IsAllowed(data []byte) (metadata any, ok bool)
	// GetID returns the stream ID
	GetID() string
}
