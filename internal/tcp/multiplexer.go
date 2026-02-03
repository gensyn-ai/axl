package tcp

type Multiplexer struct {
	sources []Stream
}

func (m *Multiplexer) AddSource(s Stream) {
	m.sources = append(m.sources, s)
}
