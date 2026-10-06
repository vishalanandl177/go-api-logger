package httpmw

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apilog "github.com/vishalanandl177/go-api-logger"
)

type reviewFlushErrorWriter struct {
	bareWriter
	err error
}

func (w *reviewFlushErrorWriter) FlushError() error { w.WriteHeader(http.StatusOK); return w.err }

type reviewDualFlushWriter struct{ reviewFlushErrorWriter }

func (w *reviewDualFlushWriter) Flush() { _ = w.FlushError() }

func TestReviewResponseControllerPreservesFlushError(t *testing.T) {
	want := errors.New("write deadline exceeded")
	original := &reviewDualFlushWriter{reviewFlushErrorWriter: reviewFlushErrorWriter{err: want}}
	capture := &captureWriter{original: original, buffer: &bodyBuffer{limit: 1024}, config: apilog.DefaultConfig()}
	if got := http.NewResponseController(wrapWriter(capture)).Flush(); !errors.Is(got, want) {
		t.Fatalf("middleware changed ResponseController.Flush error: got %v, want %v", got, want)
	}
}

func TestReviewResponseControllerFlushOnlyCannotBypassCapture(t *testing.T) {
	l, c := setup(t, nil)
	original := &reviewFlushErrorWriter{}
	Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})).ServeHTTP(original, httptest.NewRequest("GET", "/", nil))
	e := c.event(t, l)
	if e.Status != original.status || e.Response.State != "streaming" || len(e.Response.Data) != 0 {
		t.Fatalf("flush bypassed capture: wire status %d, logged status %d body state %q data %s", original.status, e.Status, e.Response.State, e.Response.Data)
	}
}

func TestReviewNativeRoutePolicyStopsResponseCaptureBeforeSanitizer(t *testing.T) {
	called := false
	l, c := setup(t, func(config *apilog.Config) {
		config.ContentTypes = []string{"text/x-private"}
		config.Rules = []apilog.Rule{{Route: "GET /private", Decision: apilog.Decision{StripResponse: true}}}
		config.Sanitizer = func(_ string, data []byte, _ []string) ([]byte, error) {
			called = true
			return []byte(`{"value":"redacted"}`), nil
		}
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /private", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/x-private")
		_, _ = w.Write([]byte("private-body"))
	})
	Middleware(l)(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/private", nil))
	e := c.event(t, l)
	if called {
		t.Error("private response was captured and delivered to sanitizer before route policy was enforced")
	}
	if e.Response.State != "omitted" || len(e.Response.Data) != 0 || !strings.Contains(e.Route, "/private") {
		t.Fatalf("policy output incorrect: %+v", e)
	}
}

func TestReviewHEADDoesNotRetainUnsentResponsePayload(t *testing.T) {
	l, c := setup(t, nil)
	server := httptest.NewServer(Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"private":"unsent-value"}`))
	})))
	defer server.Close()
	response, err := server.Client().Head(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || len(data) != 0 {
		t.Fatalf("invalid HEAD fixture: data %q err %v", data, err)
	}
	e := c.event(t, l)
	if len(e.Response.Data) != 0 || e.Response.Bytes != 0 {
		t.Fatalf("logged a response payload not sent for HEAD: %+v", e.Response)
	}
}
