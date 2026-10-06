// Independent interface-composition fixtures for the public ResponseWriter contract.
package httpmw

import (
	"io"
	"net/http"
)

func capabilityFixtures() []func(*conformanceCapabilities) http.ResponseWriter {
	return []func(*conformanceCapabilities) http.ResponseWriter{
		func(p *conformanceCapabilities) http.ResponseWriter { return struct{ http.ResponseWriter }{p} },
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
			}{p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
			}{p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Pusher
			}{p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Pusher
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.Pusher
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.Pusher
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				io.ReaderFrom
			}{p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				io.ReaderFrom
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				io.ReaderFrom
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				io.ReaderFrom
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Pusher
				io.ReaderFrom
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Pusher
				io.ReaderFrom
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.Pusher
				io.ReaderFrom
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.Pusher
				io.ReaderFrom
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.CloseNotifier
			}{p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.CloseNotifier
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.CloseNotifier
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.CloseNotifier
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Pusher
				http.CloseNotifier
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Pusher
				http.CloseNotifier
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.Pusher
				http.CloseNotifier
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.Pusher
				http.CloseNotifier
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				io.ReaderFrom
				http.CloseNotifier
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				io.ReaderFrom
				http.CloseNotifier
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				io.ReaderFrom
				http.CloseNotifier
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				io.ReaderFrom
				http.CloseNotifier
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Pusher
				io.ReaderFrom
				http.CloseNotifier
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Pusher
				io.ReaderFrom
				http.CloseNotifier
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.Pusher
				io.ReaderFrom
				http.CloseNotifier
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.Pusher
				io.ReaderFrom
				http.CloseNotifier
			}{p, p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				conformanceFlushError
			}{p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				conformanceFlushError
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				conformanceFlushError
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Pusher
				conformanceFlushError
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Pusher
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.Pusher
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.Pusher
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				io.ReaderFrom
				conformanceFlushError
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				io.ReaderFrom
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				io.ReaderFrom
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				io.ReaderFrom
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Pusher
				io.ReaderFrom
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Pusher
				io.ReaderFrom
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.Pusher
				io.ReaderFrom
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.Pusher
				io.ReaderFrom
				conformanceFlushError
			}{p, p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Pusher
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Pusher
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.Pusher
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.Pusher
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				io.ReaderFrom
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				io.ReaderFrom
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				io.ReaderFrom
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				io.ReaderFrom
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Pusher
				io.ReaderFrom
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Pusher
				io.ReaderFrom
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Hijacker
				http.Pusher
				io.ReaderFrom
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p, p}
		},
		func(p *conformanceCapabilities) http.ResponseWriter {
			return struct {
				http.ResponseWriter
				http.Flusher
				http.Hijacker
				http.Pusher
				io.ReaderFrom
				http.CloseNotifier
				conformanceFlushError
			}{p, p, p, p, p, p, p}
		},
	}
}
