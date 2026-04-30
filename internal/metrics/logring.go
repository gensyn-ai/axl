package metrics

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LogRing buffers the most recent log lines and broadcasts new lines to subscribers.
type LogRing struct {
	mu     sync.RWMutex
	cap    int
	buf    []LogLine
	head   int
	count  int
	nextID atomic.Uint64

	subs map[uint64]chan LogLine
	subID atomic.Uint64
}

type LogLine struct {
	ID    uint64    `json:"id"`
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

func NewLogRing(cap int) *LogRing {
	return &LogRing{
		cap:  cap,
		buf:  make([]LogLine, cap),
		subs: make(map[uint64]chan LogLine),
	}
}

// Write implements io.Writer for log integration. Each Write may contain
// multiple lines, in which case they are split.
func (l *LogRing) Write(p []byte) (int, error) {
	s := string(p)
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			continue
		}
		l.append(line)
	}
	return len(p), nil
}

func (l *LogRing) append(text string) {
	level := detectLevel(text)
	id := l.nextID.Add(1)
	entry := LogLine{ID: id, At: time.Now(), Level: level, Text: text}

	l.mu.Lock()
	l.buf[l.head] = entry
	l.head = (l.head + 1) % l.cap
	if l.count < l.cap {
		l.count++
	}
	subs := make([]chan LogLine, 0, len(l.subs))
	for _, ch := range l.subs {
		subs = append(subs, ch)
	}
	l.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- entry:
		default:
			// drop on full
		}
	}
}

func (l *LogRing) Snapshot(limit int) []LogLine {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n := l.count
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]LogLine, 0, n)
	start := (l.head - n + l.cap) % l.cap
	for i := 0; i < n; i++ {
		out = append(out, l.buf[(start+i)%l.cap])
	}
	return out
}

// Subscribe returns a channel that receives new log lines until Unsubscribe is called.
func (l *LogRing) Subscribe(buf int) (uint64, <-chan LogLine) {
	id := l.subID.Add(1)
	ch := make(chan LogLine, buf)
	l.mu.Lock()
	l.subs[id] = ch
	l.mu.Unlock()
	return id, ch
}

func (l *LogRing) Unsubscribe(id uint64) {
	l.mu.Lock()
	ch, ok := l.subs[id]
	delete(l.subs, id)
	l.mu.Unlock()
	if ok {
		close(ch)
	}
}

func detectLevel(text string) string {
	switch {
	case strings.Contains(text, "ERROR"), strings.Contains(text, "error:"), strings.Contains(text, " error "):
		return "error"
	case strings.Contains(text, "WARN"), strings.Contains(text, "warn:"):
		return "warn"
	case strings.Contains(text, "DEBUG"), strings.Contains(text, "debug:"):
		return "debug"
	default:
		return "info"
	}
}
