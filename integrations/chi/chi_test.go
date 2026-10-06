package chi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
	apichi "github.com/vishalanandl177/go-api-logger/integrations/chi"
	"github.com/vishalanandl177/go-api-logger/integrations/internal/testutil"
)

func TestMetadataRestrictsCaptureBeforeHandlerBodyAccess(t *testing.T) {
	for _, policy := range []string{"route rule", "group rule", "policy hook"} {
		t.Run(policy, func(t *testing.T) {
			observer := &testutil.Observer{}
			config := apilog.DefaultConfig()
			config.Observer = observer
			config.Outputs = []apilog.Output{{Name: "test", Sink: apilog.SinkFunc(func(context.Context, []apilog.Event) error { return nil })}}
			config.Security.Enabled = true
			config.Security.InspectRequest = true
			config.Security.InspectResponse = true
			config.Security.Rules = []string{"DRFSEC-006", "DRFSEC-011"}
			strip := apilog.Decision{StripRequest: true, StripResponse: true}
			switch policy {
			case "route rule":
				config.Rules = []apilog.Rule{{Route: "/v1/private/{id}", Decision: strip}}
			case "group rule":
				config.Rules = []apilog.Rule{{Group: "private", Decision: strip}}
			}
			inHandler := false
			var early []apilog.Event
			config.Policy = func(e apilog.Event) (apilog.Decision, error) {
				if inHandler {
					early = append(early, e)
				}
				if policy == "policy hook" && e.Route == "/v1/private/{id}" && e.Group == "private" {
					return strip, nil
				}
				return apilog.Decision{}, nil
			}
			logger, err := apilog.New(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = logger.Shutdown(context.Background()) })
			router := chi.NewRouter()
			router.Use(apichi.Metadata("api"))
			router.Route("/v1", func(v1 chi.Router) {
				for _, group := range []string{"private", "public"} {
					v1.With(apichi.Metadata(group)).Post("/"+group+"/{id}", func(w http.ResponseWriter, r *http.Request) {
						inHandler = true
						defer func() { inHandler = false }()
						if _, err := io.Copy(io.Discard, r.Body); err != nil {
							t.Error(err)
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"password":"private-output"}`)
					})
				}
			})
			handler := httpmw.Middleware(logger)(router)
			for _, group := range []string{"private", "public"} {
				early = nil
				request := httptest.NewRequest(http.MethodPost, "/v1/"+group+"/42", strings.NewReader(`{"input":"<script>alert(1)</script>"}`))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Body.String() != `{"password":"private-output"}` {
					t.Fatalf("middleware changed handler response: %d %s", response.Code, response.Body.String())
				}
				if len(early) < 2 {
					t.Fatalf("missing request/response capture policy observations: %d", len(early))
				}
				for _, e := range early {
					if e.Route != "/v1/"+group+"/{id}" || e.Group != group {
						t.Errorf("body capture policy ran before route enrichment: route=%q group=%q", e.Route, e.Group)
					}
				}
			}
			events := observer.Events()
			if len(events) != 2 {
				t.Fatalf("events=%d", len(events))
			}
			private, public := events[0], events[1]
			if private.Route != "/v1/private/{id}" || private.Group != "private" {
				t.Errorf("outer metadata overwrote innermost route/group: %q %q", private.Route, private.Group)
			}
			if len(private.Request.Data) != 0 || len(private.Response.Data) != 0 || private.Request.State != "omitted" || private.Response.State != "omitted" {
				t.Errorf("restricted payload retained: request=%+v response=%+v", private.Request, private.Response)
			}
			if len(private.Security) != 0 {
				t.Errorf("restricted payload reached temporary security samples: %+v", private.Security)
			}
			if len(public.Security) != 2 || len(public.Request.Data) == 0 || len(public.Response.Data) == 0 {
				t.Errorf("positive control did not capture and inspect public payloads: %+v", public)
			}
		})
	}
}

func TestMetadataFinalizesRoutesWithoutPayloads(t *testing.T) {
	logger, observer := testutil.Logger(t)
	router := chi.NewRouter()
	router.Use(apichi.Metadata("api"))
	router.With(apichi.Metadata("private")).Get("/private/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	httpmw.Middleware(logger)(router).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/private/42", nil))
	events := observer.Events()
	if len(events) != 1 || events[0].Route != "/private/{id}" || events[0].Group != "private" {
		t.Fatalf("missing final metadata without body capture: %+v", events)
	}
}
