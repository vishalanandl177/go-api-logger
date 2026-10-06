package httpmw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
)

type faultObserver struct {
	hook  string
	calls atomic.Int64
}

func (o *faultObserver) invoke(hook string) {
	if hook == o.hook && o.calls.Add(1) == 1 {
		panic("recoverable observer fault")
	}
}
func (o *faultObserver) Observe(context.Context, apilog.Event) { o.invoke("observe") }
func (o *faultObserver) Pipeline(string, apilog.OutputHealth)  { o.invoke("pipeline") }
func (o *faultObserver) RequestStarted(context.Context, apilog.Event) {
	o.invoke("request_started")
}
func (o *faultObserver) RequestFinished(context.Context, apilog.Event) {
	o.invoke("request_finished")
}
func (o *faultObserver) ObserveTiming(string, apilog.Timing) { o.invoke("timing") }

// A failed logging hook must neither change the application response nor leave
// subsequent requests unable to log after the hook becomes healthy again.
func TestRequestHookFailuresAreIsolatedAndRecoverOnNextRequest(t *testing.T) {
	for _, hook := range []string{"policy", "transform", "sanitizer", "id_generator", "route_resolver", "observe", "request_started", "request_finished", "timing", "pipeline"} {
		t.Run(hook, func(t *testing.T) {
			observer := &faultObserver{hook: hook}
			var callbackCalls atomic.Int64
			logger, output := setup(t, func(c *apilog.Config) {
				c.Observer = observer
				c.Correlation.Enabled = true
				switch hook {
				case "policy":
					c.Policy = func(e apilog.Event) (apilog.Decision, error) {
						if e.Path == "/fault" {
							callbackCalls.Add(1)
							panic("recoverable policy fault")
						}
						return apilog.Decision{}, nil
					}
				case "transform":
					c.Transform = func(e apilog.Event) (*apilog.Event, error) {
						if callbackCalls.Add(1) == 1 {
							panic("recoverable transform fault")
						}
						return &e, nil
					}
				case "sanitizer":
					c.ContentTypes = append(c.ContentTypes, "text/plain")
					c.Sanitizer = func(_ string, _ []byte, _ []string) ([]byte, error) {
						if callbackCalls.Add(1) == 1 {
							panic("recoverable sanitizer fault")
						}
						return []byte(`{"token":"request-secret"}`), nil
					}
				case "id_generator":
					c.Correlation.GenerateID = func() string {
						if callbackCalls.Add(1) == 1 {
							panic("recoverable ID generator fault")
						}
						return "healthy-request-id"
					}
				}
			})
			handlerCalls := 0
			handler := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalls++
				if hook == "route_resolver" {
					defer apilog.SetRouteResolver(r.Context(), func() (string, string, string) {
						if r.URL.Path == "/fault" {
							callbackCalls.Add(1)
							panic("recoverable route resolver fault")
						}
						return "/healthy", "", "api"
					})()
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != `{"token":"request-secret"}` {
					t.Errorf("request body changed: %q, %v", body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"token":"response-secret"}`)
			}))
			for _, path := range []string{"/fault", "/healthy"} {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"token":"request-secret"}`))
				r.Header.Set("Content-Type", "application/json")
				if hook == "sanitizer" {
					r.Header.Set("Content-Type", "text/plain")
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != http.StatusCreated || w.Body.String() != `{"token":"response-secret"}` {
					t.Fatalf("logging hook altered response: status %d, body %q", w.Code, w.Body.String())
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := logger.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if handlerCalls != 2 {
				t.Fatalf("handler calls = %d, want 2", handlerCalls)
			}
			if callbackCalls.Load() == 0 && observer.calls.Load() == 0 {
				t.Fatal("fault was not exercised")
			}
			output.mu.Lock()
			events := append([]apilog.Event(nil), output.events...)
			output.mu.Unlock()
			wantEvents := 2
			if hook == "transform" || hook == "route_resolver" {
				wantEvents = 1
			}
			if len(events) != wantEvents {
				t.Fatalf("got %d events, want %d", len(events), wantEvents)
			}
			for _, event := range events {
				data, err := json.Marshal(event)
				if err != nil || strings.Contains(string(data), "request-secret") || strings.Contains(string(data), "response-secret") {
					t.Fatalf("unsafe event following logging fault: %s, %v", data, err)
				}
				if event.Panicked || event.Status != http.StatusCreated {
					t.Fatalf("logging fault misreported as application failure: %+v", event)
				}
			}
			healthy := events[len(events)-1]
			if healthy.Path != "/healthy" || healthy.Request.State != "complete" || !strings.Contains(string(healthy.Request.Data), apilog.Filtered) {
				t.Fatalf("next request did not recover: %+v", healthy)
			}
			if hook == "id_generator" && (events[0].RequestID == "" || healthy.RequestID != "healthy-request-id") {
				t.Fatal("ID generator did not fall back then recover")
			}
			if hook == "policy" && (len(events[0].Request.Data) != 0 || len(events[0].Response.Data) != 0 || !events[0].ExportDisabled) {
				t.Fatal("failed policy did not restrict the affected event")
			}
			if hook == "sanitizer" && (len(events[0].Request.Data) != 0 || events[0].Request.State != "invalid") {
				t.Fatal("failed sanitizer retained an unsafe request")
			}
		})
	}
}

type panicReadCloser struct{ value any }

func (r panicReadCloser) Read([]byte) (int, error) { panic(r.value) }
func (panicReadCloser) Close() error               { return nil }

type panicWriteRecorder struct {
	*httptest.ResponseRecorder
	value any
}

func (w panicWriteRecorder) Write([]byte) (int, error) { panic(w.value) }

func TestApplicationPanicsSurviveLoggingHookFailures(t *testing.T) {
	for _, source := range []string{"handler", "request_reader", "response_writer"} {
		t.Run(source, func(t *testing.T) {
			logger, _ := setup(t, func(c *apilog.Config) {
				c.Transform = func(apilog.Event) (*apilog.Event, error) { panic("logging transform fault") }
				c.Observer = &faultObserver{hook: "request_finished"}
			})
			want := &struct{ origin string }{origin: source}
			r := httptest.NewRequest(http.MethodPost, "/panic", nil)
			var w http.ResponseWriter = httptest.NewRecorder()
			if source == "request_reader" {
				r.Body = panicReadCloser{value: want}
			}
			if source == "response_writer" {
				w = panicWriteRecorder{ResponseRecorder: httptest.NewRecorder(), value: want}
			}
			var got any
			func() {
				defer func() { got = recover() }()
				Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch source {
					case "handler":
						panic(want)
					case "request_reader":
						_, _ = io.ReadAll(r.Body)
					case "response_writer":
						_, _ = w.Write([]byte("response"))
					}
				})).ServeHTTP(w, r)
			}()
			if got != want {
				t.Fatalf("original application panic was replaced or swallowed: got %v, want %v", got, want)
			}
		})
	}
}

func TestNilApplicationPanicIsNotSwallowed(t *testing.T) {
	for _, setting := range []string{"0", "1"} {
		t.Run("panicnil="+setting, func(t *testing.T) {
			t.Setenv("GODEBUG", os.Getenv("GODEBUG")+",panicnil="+setting)
			logger, output := setup(t, func(c *apilog.Config) {
				c.Observer = &faultObserver{hook: "request_finished"}
			})
			returned := false
			var panicValue any
			func() {
				defer func() { panicValue = recover() }()
				Middleware(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					panic(nil)
				})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
				returned = true
			}()
			if returned {
				t.Fatal("capture swallowed panic(nil)")
			}
			if setting == "1" && panicValue != nil {
				t.Fatalf("legacy panic(nil) value changed: %v", panicValue)
			}
			if setting == "0" && panicValue == nil {
				t.Fatal("default panic(nil) value was lost")
			}
			event := output.event(t, logger)
			if !event.Panicked || event.Status != 0 {
				t.Fatalf("nil panic metadata changed: %+v", event)
			}
		})
	}
}

func TestTransientCapturePolicyFailureMinimizesEntireExchange(t *testing.T) {
	for _, fault := range []string{"error", "panic", "nil-panic"} {
		for _, failAt := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/capture-call-%d", fault, failAt), func(t *testing.T) {
				if fault == "nil-panic" {
					t.Setenv("GODEBUG", os.Getenv("GODEBUG")+",panicnil=1")
				}
				var exported collector
				calls := 0
				logger, stored := setup(t, func(c *apilog.Config) {
					c.Outputs = append(c.Outputs, apilog.Output{Name: "export", Kind: "export", Sink: &exported})
					c.Security.Enabled = true
					c.Security.InspectRequest = true
					c.Security.InspectResponse = true
					c.Security.Rules = []string{"DRFSEC-006", "DRFSEC-011"}
					c.Policy = func(e apilog.Event) (apilog.Decision, error) {
						if e.Path == "/fault" {
							calls++
							if calls == failAt {
								switch fault {
								case "error":
									return apilog.Decision{}, errors.New("temporary capture policy failure")
								case "panic":
									panic("temporary capture policy failure")
								default:
									panic(nil)
								}
							}
						}
						return apilog.Decision{}, nil
					}
				})
				const requestBody = `{"customer":"private-body","expression":"<script>"}`
				const responseBody = `{"customer":"private-response","password":"example-password"}`
				handler := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Two reads test failure before any sample and after part of
					// a request was observed; the third decision is the response.
					prefix := make([]byte, 8)
					if _, err := io.ReadFull(r.Body, prefix); err != nil {
						t.Fatal(err)
					}
					rest, err := io.ReadAll(r.Body)
					if err != nil || string(prefix)+string(rest) != requestBody {
						t.Fatalf("request changed: %q%q, %v", prefix, rest, err)
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Customer", "private-response-header")
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, responseBody)
				}))
				for _, path := range []string{"/fault", "/healthy"} {
					r := httptest.NewRequest(http.MethodPost, path+"?customer=private-query", strings.NewReader(requestBody))
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("X-Customer", "private-request-header")
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					if w.Code != http.StatusCreated || w.Body.String() != responseBody {
						t.Fatalf("response changed after policy failure: %d %q", w.Code, w.Body.String())
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := logger.Flush(ctx); err != nil {
					t.Fatal(err)
				}
				stored.mu.Lock()
				events := append([]apilog.Event(nil), stored.events...)
				stored.mu.Unlock()
				exported.mu.Lock()
				exports := append([]apilog.Event(nil), exported.events...)
				exported.mu.Unlock()
				if len(events) != 2 || len(exports) != 1 || exports[0].Path != "/healthy" {
					t.Fatalf("wrong retained events: storage=%d exports=%+v", len(events), exports)
				}
				failed := events[0]
				serialized, err := json.Marshal(failed)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(serialized), "private-") || !failed.ExportDisabled || failed.URL != "/fault" || len(failed.RequestHeaders) != 0 || len(failed.ResponseHeaders) != 0 || failed.Request.State != "omitted" || failed.Response.State != "omitted" || len(failed.Security) != 0 {
					t.Fatalf("later policy success restored restricted data: %s", serialized)
				}
				if calls <= failAt {
					t.Fatal("final policy did not return successfully after the injected fault")
				}
				healthy := events[1]
				if healthy.ExportDisabled || healthy.Request.State != "complete" || healthy.Response.State != "complete" || len(healthy.Security) != 2 {
					t.Fatalf("next request did not recover normal capture and security inspection: %+v", healthy)
				}
			})
		}
	}
}
