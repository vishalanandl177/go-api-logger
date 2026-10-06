package httpmw

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
)

type collector struct {
	mu     sync.Mutex
	events []apilog.Event
}

func (c *collector) WriteBatch(_ context.Context, e []apilog.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e...)
	return nil
}
func (c *collector) event(t *testing.T, l *apilog.Logger) apilog.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		t.Fatal("no event")
	}
	return c.events[len(c.events)-1]
}
func setup(t *testing.T, change func(*apilog.Config)) (*apilog.Logger, *collector) {
	t.Helper()
	c := &collector{}
	cfg := apilog.DefaultConfig()
	cfg.Outputs = []apilog.Output{{Name: "test", Kind: "storage", Sink: c}}
	if change != nil {
		change(&cfg)
	}
	l, err := apilog.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = l.Shutdown(ctx)
	})
	return l, c
}
func TestBodyPassThroughRedactionAndRoute(t *testing.T) {
	l, c := setup(t, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"password":"raw"}` {
			t.Error("request altered")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=private")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"token":"response-secret"}`)
	})
	r := httptest.NewRequest("POST", "/items/1?api_key=private", strings.NewReader(`{"password":"raw"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	Middleware(l)(mux).ServeHTTP(w, r)
	if w.Body.String() != `{"token":"response-secret"}` {
		t.Fatal("response altered")
	}
	e := c.event(t, l)
	if e.Route != "POST /items/{id}" || e.Status != 201 {
		t.Fatalf("bad metadata %+v", e)
	}
	serialized, _ := json.Marshal(e)
	if strings.Contains(string(serialized), "private") || strings.Contains(string(serialized), "response-secret") || strings.Contains(string(e.Request.Data), "raw") {
		t.Fatal("secret leaked")
	}
}
func TestBodyStates(t *testing.T) {
	cases := []struct {
		name  string
		read  int
		limit int
		want  string
	}{{"unread", 0, 100, "unread"}, {"partial", 3, 100, "incomplete"}, {"oversized", 100, 4, "oversized"}, {"complete", 100, 100, "complete"}}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			l, c := setup(t, func(cfg *apilog.Config) { cfg.RequestBodyLimit = tt.limit })
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.read > 0 {
					_, _ = io.ReadAll(io.LimitReader(r.Body, int64(tt.read)))
				}
				w.WriteHeader(204)
			})
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"password":"secret"}`))
			r.Header.Set("Content-Type", "application/json")
			Middleware(l)(handler).ServeHTTP(httptest.NewRecorder(), r)
			e := c.event(t, l)
			if e.Request.State != tt.want {
				t.Fatalf("got %s want %s", e.Request.State, tt.want)
			}
			if tt.want != "complete" && len(e.Request.Data) > 0 {
				t.Fatal("stored partial body")
			}
		})
	}
}
func TestPolicyBeforeBodyRead(t *testing.T) {
	l, c := setup(t, func(cfg *apilog.Config) {
		cfg.Rules = []apilog.Rule{{Route: "/private", Decision: apilog.Decision{StripRequest: true, StripResponse: true}}}
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apilog.SetRoute(r.Context(), "/private", "", "")
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"secret":"x"}`)
	})
	r := httptest.NewRequest("POST", "/private", strings.NewReader(`{"secret":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	Middleware(l)(handler).ServeHTTP(httptest.NewRecorder(), r)
	e := c.event(t, l)
	if e.Request.State != "omitted" || e.Response.State != "omitted" {
		t.Fatalf("policy ignored %+v", e)
	}
}

type bareWriter struct {
	header http.Header
	status int
	data   []byte
}

func (w *bareWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *bareWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *bareWriter) Write(p []byte) (int, error) { w.data = append(w.data, p...); return len(p), nil }
func TestOptionalInterfacesPreserved(t *testing.T) {
	for _, original := range []http.ResponseWriter{&bareWriter{}, httptest.NewRecorder()} {
		cfg := apilog.DefaultConfig()
		c := &captureWriter{original: original, config: cfg, buffer: &bodyBuffer{limit: 1024}}
		wrapped := wrapWriter(c)
		_, before := original.(http.Flusher)
		_, after := wrapped.(http.Flusher)
		if before != after {
			t.Fatal("flusher changed")
		}
		_, before = original.(http.Hijacker)
		_, after = wrapped.(http.Hijacker)
		if before != after {
			t.Fatal("hijacker changed")
		}
		if wrapped.(interface{ Unwrap() http.ResponseWriter }).Unwrap() != original {
			t.Fatal("unwrap changed")
		}
	}
}
func TestImplicitStatusRepeatedHeadersAndPanics(t *testing.T) {
	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
		panics  bool
	}{{"implicit", func(w http.ResponseWriter, r *http.Request) {}, 200, false}, {"repeated", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201); w.WriteHeader(500) }, 201, false}, {"panic", func(w http.ResponseWriter, r *http.Request) { panic("handler panic") }, 0, true}, {"partial panic", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202); panic("handler panic") }, 202, true}} {
		t.Run(tt.name, func(t *testing.T) {
			l, c := setup(t, nil)
			didPanic := false
			func() {
				defer func() { didPanic = recover() != nil }()
				Middleware(l)(tt.handler).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
			}()
			e := c.event(t, l)
			if didPanic != tt.panics || e.Panicked != tt.panics || e.Status != tt.status {
				t.Fatalf("panic/status changed %+v", e)
			}
		})
	}
}
func TestHTTP2AndSSEFlush(t *testing.T) {
	l, c := setup(t, nil)
	release := make(chan struct{})
	server := httptest.NewUnstartedServer(Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		<-release
	})))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	if resp.ProtoMajor != 2 {
		t.Error("HTTP2 not negotiated")
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Errorf("flush delayed %q %v", line, err)
	}
	close(release)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	e := c.event(t, l)
	if e.Response.State != "streaming" || len(e.Response.Data) != 0 {
		t.Fatalf("stream retained %+v", e.Response)
	}
}
func TestHijackPassThrough(t *testing.T) {
	l, c := setup(t, nil)
	done := make(chan struct{})
	wrapped := Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\nhello")
		_ = rw.Flush()
	}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { wrapped.ServeHTTP(w, r); close(done) }))
	defer server.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n")
	b, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if !strings.Contains(string(b), "hello") {
		t.Fatal(string(b))
	}
	e := c.event(t, l)
	if e.Response.State != "upgraded" {
		t.Fatalf("upgrade not recorded %+v", e)
	}
}
func TestGzipPassThroughMetadataOnly(t *testing.T) {
	l, c := setup(t, nil)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = io.WriteString(gz, `{"password":"secret"}`)
		_ = gz.Close()
	})
	w := httptest.NewRecorder()
	Middleware(l)(handler).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	gz, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(gz)
	if string(b) != `{"password":"secret"}` {
		t.Fatal("compression altered")
	}
	e := c.event(t, l)
	if e.Response.State != "encoded" || len(e.Response.Data) != 0 {
		t.Fatal("compressed bytes retained")
	}
}
func TestTrustedProxyAndCorrelation(t *testing.T) {
	l, c := setup(t, func(cfg *apilog.Config) { cfg.TrustedProxies = []string{"10.0.0.0/8"}; cfg.Correlation.Enabled = true })
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:8080"
	r.Header.Set("X-Forwarded-For", "spoofed, 203.0.113.9, 10.0.0.2")
	r.Header.Set("X-Request-ID", "request-1")
	r.Header.Set("traceparent", "00-01234567890123456789012345678901-0123456789012345-01")
	Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apilog.RequestID(r.Context()) != "request-1" {
			t.Error("context missing")
		}
	})).ServeHTTP(httptest.NewRecorder(), r)
	e := c.event(t, l)
	if e.ClientIP != "203.0.113.9" || e.RequestID != "request-1" || e.TraceID == "" {
		t.Fatalf("bad correlation %+v", e)
	}
	r.RemoteAddr = "192.0.2.1:8"
	if got := clientIP(r, []string{"10.0.0.0/8"}); got != "192.0.2.1" {
		t.Fatal("trusted untrusted header")
	}
}
func BenchmarkMiddleware(b *testing.B) {
	for _, mode := range []string{"baseline", "metadata", "body"} {
		b.Run(mode, func(b *testing.B) {
			cfg := apilog.DefaultConfig()
			cfg.MetadataOnly = mode == "metadata"
			l, _ := apilog.New(cfg)
			defer l.Shutdown(context.Background())
			var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
			})
			if mode != "baseline" {
				handler = Middleware(l)(handler)
			}
			b.ReportAllocs()
			for b.Loop() {
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
			}
		})
	}
}
