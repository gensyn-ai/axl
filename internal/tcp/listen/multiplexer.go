package listen

type Multiplexer struct {
	sources      []Stream
	requestTypes map[string]StreamFactory
}

type StreamFactory func() any

func NewMultiplexer() *Multiplexer {
	return &Multiplexer{
		requestTypes: make(map[string]StreamFactory),
	}
}

func (m *Multiplexer) AddSource(s Stream, factory StreamFactory) {
	m.sources = append(m.sources, s)
	m.requestTypes[s.GetID()] = factory
}
func (m *Multiplexer) GetFactory(streamID string) StreamFactory {
	return m.requestTypes[streamID]
}
