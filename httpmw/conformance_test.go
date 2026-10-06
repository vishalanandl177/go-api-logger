package httpmw

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
)

var conformanceWriteError = errors.New("underlying write failed")

// protocolWriter implements final status and body-forbidden rules, including a
// controllable partial write. It is independent of the capture implementation.
type protocolWriter struct {
	header    http.Header
	status    int
	data      []byte
	remaining int
	writeErr  error
}

func (w *protocolWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *protocolWriter) WriteHeader(status int) {
	if status < 100 || status > 999 {
		panic("invalid status")
	}
	if status >= 200 && w.status == 0 {
		w.status = status
	}
}
func (w *protocolWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.status == 204 || w.status == 304 {
		return 0, http.ErrBodyNotAllowed
	}
	n := len(p)
	if w.remaining >= 0 && n > w.remaining {
		n = w.remaining
	}
	w.data = append(w.data, p[:n]...)
	if w.remaining >= 0 {
		w.remaining -= n
	}
	if n < len(p) {
		return n, w.writeErr
	}
	return n, nil
}

func TestConformancePartialWriteErrorPassesThrough(t *testing.T) {
	l, c := setup(t, nil)
	original := &protocolWriter{remaining: 5, writeErr: conformanceWriteError}
	var gotN int
	var gotErr error
	Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		gotN, gotErr = w.Write([]byte(`{"token":"private"}`))
	})).ServeHTTP(original, httptest.NewRequest("GET", "/", nil))
	if gotN != 5 || gotErr != conformanceWriteError {
		t.Fatalf("Write result changed: %d %v", gotN, gotErr)
	}
	e := c.event(t, l)
	if e.Status != 200 || e.Response.Bytes != 5 || len(e.Response.Data) != 0 || e.Response.State != "invalid" {
		t.Fatalf("partial JSON was retained or bytes lost: %+v", e)
	}
}

func TestConformanceInformationalHeadersReachClientBeforeFinalStatus(t *testing.T) {
	l, c := setup(t, nil)
	server := httptest.NewServer(Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", "</first.css>; rel=preload")
		w.WriteHeader(http.StatusContinue)
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Del("Link")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Final", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})))
	defer server.Close()
	var codes []int
	trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
		codes = append(codes, code)
		if header.Get("Link") == "" {
			t.Error("informational headers lost")
		}
		return nil
	}}
	r, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), "GET", server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(codes, []int{100, 103}) || resp.StatusCode != 201 || string(body) != `{"ok":true}` {
		t.Fatalf("wire response changed: codes=%v final=%d body=%s", codes, resp.StatusCode, body)
	}
	e := c.event(t, l)
	if e.Status != 201 || e.ResponseHeaders["X-Final"][0] != "yes" || len(e.ResponseHeaders["Link"]) != 0 {
		t.Fatalf("wrong final header snapshot: %+v", e)
	}
}

func TestConformanceRequestCancellationDoesNotCancelAsyncDelivery(t *testing.T) {
	delivered := make(chan error, 1)
	l, _ := setup(t, func(c *apilog.Config) {
		c.Queue.BatchSize = 1
		c.Outputs[0].Sink = apilog.SinkFunc(func(ctx context.Context, _ []apilog.Event) error { delivered <- ctx.Err(); return nil })
	})
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		select {
		case <-r.Context().Done():
		default:
			t.Error("request cancellation was replaced")
		}
		if r.Context().Err() != context.Canceled {
			t.Errorf("wrong cancellation %v", r.Context().Err())
		}
		w.WriteHeader(499)
	})).ServeHTTP(httptest.NewRecorder(), r)
	select {
	case err := <-delivered:
		if err != nil {
			t.Fatalf("sink inherited canceled request context: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request never delivered")
	}
}

type conformanceBody struct {
	read              bool
	closed            int
	readErr, closeErr error
}

func (b *conformanceBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, b.readErr
	}
	b.read = true
	return copy(p, []byte(`{"x":`)), b.readErr
}
func (b *conformanceBody) Close() error { b.closed++; return b.closeErr }

func TestConformanceRequestBodyReadAndCloseErrorsPassThrough(t *testing.T) {
	l, c := setup(t, nil)
	readErr, closeErr := errors.New("read fault"), errors.New("close fault")
	body := &conformanceBody{readErr: readErr, closeErr: closeErr}
	r := httptest.NewRequest("POST", "/", nil)
	r.Body = body
	r.ContentLength = -1
	r.Header.Set("Content-Type", "application/json")
	Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := make([]byte, 32)
		n, err := r.Body.Read(p)
		if n != 5 || err != readErr || string(p[:n]) != `{"x":` {
			t.Fatalf("Read result changed: %d %v %q", n, err, p[:n])
		}
		if err := r.Body.Close(); err != closeErr {
			t.Fatalf("Close error changed: %v", err)
		}
		w.WriteHeader(400)
	})).ServeHTTP(httptest.NewRecorder(), r)
	if body.closed != 1 {
		t.Fatalf("middleware changed body ownership: Close calls=%d", body.closed)
	}
	e := c.event(t, l)
	if e.Request.State != "incomplete" || e.Request.Bytes != 5 || len(e.Request.Data) != 0 {
		t.Fatalf("read error body retained: %+v", e.Request)
	}
}

func TestConformanceReaderFromPreservesSourceFailureAndObservedBytes(t *testing.T) {
	l, c := setup(t, nil)
	readErr := errors.New("response source failed")
	source := &conformanceBody{readErr: readErr}
	probe := &conformanceCapabilities{protocolWriter: protocolWriter{remaining: -1}}
	original := struct {
		http.ResponseWriter
		io.ReaderFrom
	}{probe, probe}
	Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n, err := io.Copy(w, struct{ io.Reader }{source})
		if n != 5 || err != readErr {
			t.Fatalf("ReaderFrom changed the source result: %d %v", n, err)
		}
	})).ServeHTTP(original, httptest.NewRequest("GET", "/", nil))
	e := c.event(t, l)
	if string(probe.data) != `{"x":` || e.Response.Bytes != 5 || len(e.Response.Data) != 0 {
		t.Fatalf("ReaderFrom lost capture or retained partial JSON: wire=%q body=%+v", probe.data, e.Response)
	}
}

type conformanceControllerWriter struct {
	protocolWriter
	readDeadline, writeDeadline time.Time
	duplex                      bool
}

func (w *conformanceControllerWriter) SetReadDeadline(deadline time.Time) error {
	w.readDeadline = deadline
	return conformanceWriteError
}
func (w *conformanceControllerWriter) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadline = deadline
	return conformanceWriteError
}
func (w *conformanceControllerWriter) EnableFullDuplex() error {
	w.duplex = true
	return conformanceWriteError
}

func TestConformanceResponseControllerDeadlinesAndDuplexReachOriginal(t *testing.T) {
	original := &conformanceControllerWriter{}
	capture := &captureWriter{original: original, buffer: &bodyBuffer{limit: 128}, config: apilog.DefaultConfig()}
	controller := http.NewResponseController(wrapWriter(capture))
	deadline := time.Unix(2000000000, 0)
	if err := controller.SetReadDeadline(deadline); err != conformanceWriteError || original.readDeadline != deadline {
		t.Fatalf("read deadline changed: %v", err)
	}
	if err := controller.SetWriteDeadline(deadline); err != conformanceWriteError || original.writeDeadline != deadline {
		t.Fatalf("write deadline changed: %v", err)
	}
	if err := controller.EnableFullDuplex(); err != conformanceWriteError || !original.duplex {
		t.Fatalf("duplex result changed: %v", err)
	}
	if capture.status != 0 || len(capture.buffer.data) != 0 {
		t.Fatal("controller configuration committed a response")
	}
}

func TestConformanceTraceparentValidation(t *testing.T) {
	const trace = "4bf92f3577b34da6a3ce929d0e0e4736"
	const span = "00f067aa0ba902b7"
	valid := "00-" + trace + "-" + span + "-01"
	cases := []struct{ name, value, want string }{
		{"valid", valid, trace}, {"unsampled", "00-" + trace + "-" + span + "-00", trace},
		{"upper trace", "00-" + strings.ToUpper(trace) + "-" + span + "-01", ""},
		{"upper span", "00-" + trace + "-" + strings.ToUpper(span) + "-01", ""},
		{"upper flags", "00-" + trace + "-" + span + "-0A", ""},
		{"upper version", "FF-" + trace + "-" + span + "-01", ""},
		{"forbidden version", "ff-" + trace + "-" + span + "-01", ""},
		{"zero trace", "00-" + strings.Repeat("0", 32) + "-" + span + "-01", ""},
		{"zero span", "00-" + trace + "-" + strings.Repeat("0", 16) + "-01", ""},
		{"short", valid[:54], ""}, {"version00 suffix", valid + "-extra", ""},
		{"future base", "01-" + trace + "-" + span + "-01", trace},
		{"future extension", "01-" + trace + "-" + span + "-01-extra", trace},
		{"future no separator", "01-" + trace + "-" + span + "-01extra", ""},
		{"trailing space", valid + " ", ""}, {"nonhex", "00-z" + trace[1:] + "-" + span + "-01", ""},
	}
	cfg := apilog.DefaultConfig()
	cfg.Correlation.TraceIDHeaders = []string{"TrAcEpArEnT"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("traceparent", tc.value)
			if got := traceID(r, cfg); got != tc.want {
				t.Fatalf("traceID(%q)=%q want %q", tc.value, got, tc.want)
			}
		})
	}
}

type conformanceCapabilities struct {
	protocolWriter
	flushes, flushErrors, pushes, hijacks int
	closed                                chan bool
}

func (p *conformanceCapabilities) Flush() { p.flushes++; p.WriteHeader(200) }
func (p *conformanceCapabilities) FlushError() error {
	p.flushErrors++
	p.WriteHeader(200)
	return conformanceWriteError
}
func (p *conformanceCapabilities) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	p.hijacks++
	return nil, nil, conformanceWriteError
}
func (p *conformanceCapabilities) Push(target string, opts *http.PushOptions) error {
	if target != "/asset" || opts.Header.Get("X-Test") != "yes" {
		return errors.New("push arguments changed")
	}
	p.pushes++
	return conformanceWriteError
}
func (p *conformanceCapabilities) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{p}, r)
}
func (p *conformanceCapabilities) CloseNotify() <-chan bool { return p.closed }

type conformanceFlushError interface{ FlushError() error }

func capabilityMask(w http.ResponseWriter) int {
	mask := 0
	if _, ok := w.(http.Flusher); ok {
		mask |= 1
	}
	if _, ok := w.(http.Hijacker); ok {
		mask |= 2
	}
	if _, ok := w.(http.Pusher); ok {
		mask |= 4
	}
	if _, ok := w.(io.ReaderFrom); ok {
		mask |= 8
	}
	if _, ok := w.(http.CloseNotifier); ok {
		mask |= 16
	}
	if _, ok := w.(conformanceFlushError); ok {
		mask |= 32
	}
	return mask
}

func TestConformanceEveryOptionalInterfaceCombination(t *testing.T) {
	for mask, construct := range capabilityFixtures() {
		t.Run(fmt.Sprintf("%06b", mask), func(t *testing.T) {
			probe := &conformanceCapabilities{protocolWriter: protocolWriter{remaining: -1}, closed: make(chan bool)}
			original := construct(probe)
			if got := capabilityMask(original); got != mask {
				t.Fatalf("bad test fixture: mask=%d want %d", got, mask)
			}
			capture := &captureWriter{original: original, buffer: &bodyBuffer{limit: 128}, config: apilog.DefaultConfig()}
			wrapped := wrapWriter(capture)
			if got := capabilityMask(wrapped); got != mask {
				t.Fatalf("optional interface set changed: got %06b want %06b", got, mask)
			}
			wrapped.Header().Set("Content-Type", "application/json")
			if rf, ok := wrapped.(io.ReaderFrom); ok {
				n, err := rf.ReadFrom(struct{ io.Reader }{strings.NewReader(`{"ok":true}`)})
				if err != nil || n != 11 || string(probe.data) != `{"ok":true}` || capture.buffer.bytes != 11 {
					t.Fatalf("ReaderFrom bypassed capture: n=%d err=%v wire=%q observed=%d", n, err, probe.data, capture.buffer.bytes)
				}
			}
			if f, ok := wrapped.(http.Flusher); ok {
				f.Flush()
				if probe.flushes != 1 {
					t.Fatal("Flush not delegated")
				}
			}
			if f, ok := wrapped.(conformanceFlushError); ok {
				if err := f.FlushError(); err != conformanceWriteError || probe.flushErrors != 1 {
					t.Fatalf("FlushError not preserved: %v", err)
				}
			}
			if p, ok := wrapped.(http.Pusher); ok {
				if err := p.Push("/asset", &http.PushOptions{Header: http.Header{"X-Test": []string{"yes"}}}); err != conformanceWriteError || probe.pushes != 1 {
					t.Fatalf("Push changed: %v", err)
				}
			}
			if h, ok := wrapped.(http.Hijacker); ok {
				_, _, err := h.Hijack()
				if err != conformanceWriteError || probe.hijacks != 1 {
					t.Fatalf("Hijack changed: %v", err)
				}
			}
			if n, ok := wrapped.(http.CloseNotifier); ok {
				if n.CloseNotify() != probe.closed {
					t.Fatal("CloseNotify channel replaced")
				}
			}
			if unwrapped := wrapped.(interface{ Unwrap() http.ResponseWriter }).Unwrap(); unwrapped != original {
				t.Fatal("ResponseController unwrap changed")
			}
		})
	}
}

func FuzzCaptureBoundary(f *testing.F) {
	for _, seed := range []struct {
		body               string
		status             uint16
		limit, read, write uint16
		head               bool
	}{
		{`{"token":"private"}`, 200, 128, 128, 128, false}, {`{"ok":true}`, 201, 4, 128, 128, false},
		{`{"password":"partial`, 500, 64, 5, 9, false}, {"", 204, 1, 0, 0, false},
		{`{"ok":true}`, 304, 128, 128, 128, false}, {`{"private":"not sent"}`, 200, 128, 128, 128, true},
	} {
		f.Add([]byte(seed.body), seed.status, seed.limit, seed.read, seed.write, seed.head)
	}
	f.Fuzz(func(t *testing.T, data []byte, statusCode, limitArg, readArg, writeArg uint16, head bool) {
		if len(data) > 2048 {
			t.Skip()
		}
		limit := 1 + int(limitArg%256)
		readLimit := min(int(readArg), len(data))
		writeLimit := min(int(writeArg), len(data))
		statuses := []int{200, 201, 204, 304, 400, 500}
		status := statuses[int(statusCode)%len(statuses)]
		l, c := setup(t, func(config *apilog.Config) { config.RequestBodyLimit = limit; config.ResponseBodyLimit = limit })
		method := "POST"
		if head {
			method = "HEAD"
		}
		r := httptest.NewRequest(method, "/capture", bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		original := &protocolWriter{remaining: writeLimit, writeErr: conformanceWriteError}
		Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			read, err := io.ReadAll(io.LimitReader(r.Body, int64(readLimit)))
			if err != nil || !bytes.Equal(read, data[:readLimit]) {
				t.Fatalf("request bytes changed: %q %v", read, err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			n, err := w.Write(data)
			if status == 204 || status == 304 {
				if n != 0 || err != http.ErrBodyNotAllowed {
					t.Fatal("body-forbidden response changed")
				}
			} else if n != writeLimit || (writeLimit < len(data) && err != conformanceWriteError) {
				t.Fatalf("write result changed: n=%d err=%v", n, err)
			}
		})).ServeHTTP(original, r)
		e := c.event(t, l)
		if e.Status != status || e.Request.Bytes != int64(readLimit) {
			t.Fatalf("metadata changed: %+v", e)
		}
		if head {
			if len(e.Response.Data) != 0 || e.Response.Bytes != 0 {
				t.Fatal("HEAD payload retained")
			}
		} else if e.Response.Bytes != int64(len(original.data)) {
			t.Fatal("response byte count changed")
		}
		for _, body := range []apilog.Body{e.Request, e.Response} {
			if len(body.Data) > limit {
				t.Fatal("body bound exceeded")
			}
			if len(body.Data) > 0 {
				clean, err := apilog.SanitizeJSON(body.Data, nil)
				if err != nil || !bytes.Equal(clean, body.Data) {
					t.Fatalf("body is not canonical redacted JSON: %s", body.Data)
				}
			}
		}
		if readLimit < len(data) && len(e.Request.Data) > 0 {
			t.Fatal("partial request JSON retained")
		}
	})
}
