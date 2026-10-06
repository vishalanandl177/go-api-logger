// Package httpmw observes net/http exchanges without owning the server.
package httpmw

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
)

type bodyBuffer struct {
	mu                 sync.Mutex
	limit              int
	data               []byte
	bytes              int64
	state, contentType string
	eof                bool
	allowed            func() bool
	sample             []byte
	sampleLimit        int
	overhead           func(time.Duration)
}

func (b *bodyBuffer) append(data []byte) {
	start := time.Now()
	if b.overhead != nil {
		defer func() { b.overhead(time.Since(start)) }()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bytes += int64(len(data))
	if b.allowed != nil && !b.allowed() {
		b.state = "omitted"
		b.data = nil
		b.sample = nil
		return
	}
	if b.sampleLimit > len(b.sample) {
		b.sample = append(b.sample, data[:min(len(data), b.sampleLimit-len(b.sample))]...)
	}
	if b.state != "" {
		return
	}
	if int64(len(b.data))+int64(len(data)) > int64(b.limit) {
		b.state = "oversized"
		b.data = nil
		return
	}
	b.data = append(b.data, data...)
}
func (b *bodyBuffer) omit(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = reason
	b.data = nil
}
func (b *bodyBuffer) snapshot(complete bool) apilog.Body {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.state
	if state == "" {
		switch {
		case !complete && !b.eof:
			state = "incomplete"
			if b.bytes == 0 {
				state = "unread"
			}
		case b.bytes == 0:
			state = "empty"
		default:
			state = "complete"
		}
	}
	var data []byte
	if state == "complete" {
		data = append([]byte(nil), b.data...)
	}
	return apilog.Body{Data: data, State: state, Bytes: b.bytes, ContentType: b.contentType}
}

func (b *bodyBuffer) sampleBytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.sample...)
}

type observedBody struct {
	original io.ReadCloser
	buffer   *bodyBuffer
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.original.Read(p)
	if n > 0 {
		b.buffer.append(p[:n])
	}
	if err == io.EOF {
		b.buffer.mu.Lock()
		b.buffer.eof = true
		b.buffer.mu.Unlock()
	}
	return n, err
}
func (b *observedBody) Close() error { return b.original.Close() }

type captureWriter struct {
	original http.ResponseWriter
	buffer   *bodyBuffer
	config   apilog.Config
	mu       sync.Mutex
	status   int
	headers  http.Header
	hijacked bool
	method   string
}

func (c *captureWriter) Header() http.Header         { return c.original.Header() }
func (c *captureWriter) Unwrap() http.ResponseWriter { return c.original }
func (c *captureWriter) commit(status int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status != 0 || status < 200 && status != 101 {
		return
	}
	c.status = status
	c.headers = c.original.Header().Clone()
	c.buffer.mu.Lock()
	c.buffer.contentType = c.headers.Get("Content-Type")
	c.buffer.mu.Unlock()
	if state := bodyState(c.headers, c.config); state != "" {
		c.buffer.omit(state)
	}
}
func (c *captureWriter) WriteHeader(status int) {
	// Delegate first so invalid statuses panic just as the underlying writer does.
	// Headers are snapshotted before delegation, since a server may normalize them.
	if status < 100 || status > 999 {
		c.original.WriteHeader(status)
		return
	}
	c.commit(status)
	c.original.WriteHeader(status)
}
func (c *captureWriter) Write(p []byte) (int, error) {
	c.commit(http.StatusOK)
	n, err := c.original.Write(p)
	if n > 0 && c.method != http.MethodHead {
		c.buffer.append(p[:n])
	}
	return n, err
}
func bodyState(headers http.Header, c apilog.Config) string {
	if headers.Get("Content-Encoding") != "" && headers.Get("Content-Encoding") != "identity" {
		return "encoded"
	}
	if headers.Get("Content-Disposition") != "" {
		return "file"
	}
	ct, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil {
		return "unsupported"
	}
	if ct == "text/event-stream" {
		return "streaming"
	}
	for _, supported := range c.ContentTypes {
		if ct == supported || (supported == "application/*+json" && strings.HasPrefix(ct, "application/") && strings.HasSuffix(ct, "+json")) {
			return ""
		}
	}
	return "unsupported"
}

// Middleware must wrap framework recovery and error rendering. Existing tracing
// middleware should wrap this middleware when active-span enrichment is needed.
func Middleware(logger *apilog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		panic("httpmw: nil logger")
	}
	cfg := logger.Config()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rawURL := r.URL.RequestURI()
			if cfg.PathMode == "path" {
				rawURL = r.URL.Path
			}
			if cfg.PathMode == "absolute" {
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}
				rawURL = scheme + "://" + r.Host + r.URL.RequestURI()
			}
			e := apilog.Event{Time: started.UTC(), Method: r.Method, URL: rawURL, Path: r.URL.Path, Protocol: r.Proto, ClientIP: clientIP(r, cfg.TrustedProxies), RequestHeaders: boundedHeaders(r.Header)}
			if cfg.Correlation.Enabled {
				e.RequestID = requestID(r, cfg)
				e.TraceID = traceID(r, cfg)
			}
			ctx, exchange := logger.Begin(r.Context(), e)
			r = r.WithContext(ctx)
			request := &bodyBuffer{limit: cfg.RequestBodyLimit, contentType: r.Header.Get("Content-Type"), state: bodyState(r.Header, cfg), allowed: func() bool {
				if r.Pattern != "" {
					apilog.SetRoutePattern(ctx, r.Pattern)
				}
				return exchange.CaptureAllowed(true)
			}}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = &observedBody{r.Body, request}
			} else {
				request.state = "empty"
				request.eof = true
			}
			response := &bodyBuffer{limit: cfg.ResponseBodyLimit, allowed: func() bool {
				if r.Pattern != "" {
					apilog.SetRoutePattern(ctx, r.Pattern)
				}
				return exchange.CaptureAllowed(false)
			}}
			request.overhead = exchange.AddOverhead
			response.overhead = exchange.AddOverhead
			if cfg.Security.Enabled && cfg.Security.InspectRequest {
				request.sampleLimit = cfg.Security.SampleBytes
			}
			if cfg.Security.Enabled && cfg.Security.InspectResponse {
				response.sampleLimit = cfg.Security.SampleBytes
			}
			if len(cfg.Outputs) == 0 {
				request.state = "omitted"
				response.state = "omitted"
			}
			capture := &captureWriter{original: w, buffer: response, config: cfg, method: r.Method}
			completed := false
			defer func() {
				panicked := recover()
				if r.Pattern != "" {
					apilog.SetRoutePattern(ctx, r.Pattern)
				}
				capture.mu.Lock()
				status, headers, hijacked := capture.status, capture.headers, capture.hijacked
				capture.mu.Unlock()
				if status == 0 && completed && !hijacked {
					status = 200
					headers = w.Header().Clone()
				}
				e.Status = status
				e.ResponseHeaders = boundedHeaders(headers)
				e.Panicked = !completed
				request.mu.Lock()
				readBytes := request.bytes
				request.mu.Unlock()
				e.Request = request.snapshot(r.ContentLength >= 0 && readBytes == r.ContentLength)
				e.Response = response.snapshot(completed)
				if r.Method == http.MethodHead {
					e.Response.Data = nil
					e.Response.Bytes = 0
					e.Response.State = "header_only"
				}
				exchange.InspectSamples(request.sampleBytes(), response.sampleBytes())
				// Logging faults must never replace a handler panic or response.
				func() { defer func() { _ = recover() }(); exchange.Finish(e) }()
				if !completed {
					panic(panicked)
				}
			}()
			next.ServeHTTP(wrapWriter(capture), r)
			completed = true
		})
	}
}
func boundedHeaders(h http.Header) map[string][]string {
	out := map[string][]string{}
	remaining := 16 << 10
	for key, values := range h {
		if remaining <= 0 || len(out) >= 128 {
			break
		}
		if len(key) > 256 {
			continue
		}
		for _, v := range values {
			if len(key) >= remaining {
				break
			}
			if len(v) > remaining-len(key) {
				v = v[:remaining-len(key)]
			}
			out[key] = append(out[key], v)
			remaining -= len(key) + len(v)
		}
	}
	return out
}
func clientIP(r *http.Request, trusted []string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	peer = peer.Unmap()
	isTrusted := func(ip netip.Addr) bool {
		for _, s := range trusted {
			p, _ := netip.ParsePrefix(s)
			if p.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !isTrusted(peer) {
		return peer.String()
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	current := peer
	for i := len(chain) - 1; i >= 0; i-- {
		if !isTrusted(current) {
			break
		}
		ip, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return peer.String()
		}
		current = ip.Unmap()
	}
	return current.String()
}
func validID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r)) {
			return false
		}
	}
	return true
}
func requestID(r *http.Request, c apilog.Config) string {
	for _, h := range c.Correlation.RequestIDHeaders {
		if id := r.Header.Get(h); validID(id) {
			return id
		}
	}
	if c.Correlation.GenerateID != nil {
		var id string
		func() { defer func() { _ = recover() }(); id = c.Correlation.GenerateID() }()
		if validID(id) {
			return id
		}
	}
	var id [16]byte
	_, _ = rand.Read(id[:])
	return hex.EncodeToString(id[:])
}
func lowerHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func traceID(r *http.Request, c apilog.Config) string {
	for _, h := range c.Correlation.TraceIDHeaders {
		value := r.Header.Get(h)
		if !strings.EqualFold(h, "traceparent") {
			if validID(value) {
				return value
			}
			continue
		}
		if len(value) < 55 || value[2] != '-' || value[35] != '-' || value[52] != '-' {
			continue
		}
		version, id, parent, flags := value[:2], value[3:35], value[36:52], value[53:55]
		if version == "ff" || !lowerHex(version) || !lowerHex(id) || !lowerHex(parent) || !lowerHex(flags) || id == strings.Repeat("0", 32) || parent == strings.Repeat("0", 16) {
			continue
		}
		if version == "00" && len(value) != 55 {
			continue
		}
		if version != "00" && len(value) > 55 && value[55] != '-' {
			continue
		}
		return id
	}
	return ""
}
