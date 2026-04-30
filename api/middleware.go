package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gensyn-ai/axl/internal/metrics"
)

// TimingMiddleware records per-route request counts and latency histograms.
// Routes are normalized: /mcp/<peer>/<service> → /mcp, /a2a/<peer> → /a2a.
func TimingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		route := normalizeRoute(r.URL.Path)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		if m := metrics.Default; m != nil {
			m.Gauge("http_in_flight").Inc()
			defer m.Gauge("http_in_flight").Dec()
		}

		next.ServeHTTP(sw, r)

		if m := metrics.Default; m != nil {
			elapsed := time.Since(start)
			m.Counter("http_requests_total").Inc()
			m.Counter("http_requests_route_" + sanitize(route) + "_total").Inc()
			m.Counter("http_requests_status_" + strconv.Itoa(sw.status/100) + "xx_total").Inc()
			m.Histogram("http_request_latency_" + sanitize(route)).Observe(elapsed)
			m.Histogram("http_request_latency").Observe(elapsed)
		}
	})
}

func normalizeRoute(path string) string {
	switch {
	case strings.HasPrefix(path, "/mcp/"):
		return "/mcp"
	case strings.HasPrefix(path, "/a2a/"):
		return "/a2a"
	case strings.HasPrefix(path, "/dashboard/api/"):
		return "/dashboard/api"
	case strings.HasPrefix(path, "/dashboard"):
		return "/dashboard"
	default:
		return path
	}
}

func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32)
		case c == '/' || c == '-' || c == '.':
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "root"
	}
	if out[0] == '_' {
		out = out[1:]
	}
	if len(out) == 0 {
		return "root"
	}
	return string(out)
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader atomic.Bool
}

func (s *statusWriter) WriteHeader(code int) {
	if s.wroteHeader.CompareAndSwap(false, true) {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wroteHeader.CompareAndSwap(false, true)
	return s.ResponseWriter.Write(b)
}

// Flush exposes http.Flusher when the underlying writer supports it (needed for SSE).
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
