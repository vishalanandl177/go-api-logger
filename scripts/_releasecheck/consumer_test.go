package consumer_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/dashboard"
	"github.com/vishalanandl177/go-api-logger/httpmw"
	_ "github.com/vishalanandl177/go-api-logger/integrations/chi"
	_ "github.com/vishalanandl177/go-api-logger/integrations/echov4"
	_ "github.com/vishalanandl177/go-api-logger/integrations/echov5"
	_ "github.com/vishalanandl177/go-api-logger/integrations/gin"
	_ "github.com/vishalanandl177/go-api-logger/integrations/gorm"
	_ "github.com/vishalanandl177/go-api-logger/integrations/otel"
	_ "github.com/vishalanandl177/go-api-logger/integrations/pgx"
	_ "github.com/vishalanandl177/go-api-logger/integrations/prometheus"
	_ "github.com/vishalanandl177/go-api-logger/integrations/sentry"
	apisql "github.com/vishalanandl177/go-api-logger/integrations/sql"
	"github.com/vishalanandl177/go-api-logger/storage"
	"modernc.org/sqlite"
)

type authenticatedAdmin struct{}

func TestPublishedConsumerCaptureProfileAndDashboard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	filename := filepath.Join(t.TempDir(), "consumer-logs.db")
	store, logDB, err := storage.OpenSQLite(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer logDB.Close()
	if err = store.Check(ctx); err == nil {
		t.Fatal("opening a store silently created its schema")
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Check(ctx); err != nil {
		t.Fatal(err)
	}
	connector, err := sqlite.NewConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	appDB := apisql.OpenDB(connector)
	appDB.SetMaxOpenConns(1)
	defer appDB.Close()
	if err = appDB.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := apilog.DefaultConfig()
	cfg.Outputs = []apilog.Output{{Name: "database", Kind: "storage", Sink: store}}
	cfg.Profile.Enabled = true
	cfg.Correlation.Enabled = true
	cfg.SkipPaths = []string{"/api-logs"}
	logger, err := apilog.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := logger.Shutdown(shutdown); err != nil {
			t.Error(err)
		}
	}()
	board, err := dashboard.New(store, dashboard.Options{Authorize: func(r *http.Request, action dashboard.Action) bool {
		admin, _ := r.Context().Value(authenticatedAdmin{}).(bool)
		return admin && action == dashboard.View
	}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Name, Password, Token string }
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		var name string
		if appDB.QueryRowContext(r.Context(), "SELECT ?", input.Name).Scan(&name) != nil {
			http.Error(w, "query failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"name": name, "token": "response-secret"})
	})
	mux.Handle("/api-logs/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This credential exists only inside this local httptest server.
		username, password, ok := r.BasicAuth()
		admin := ok && username == "admin" && password == "consumer-test-password"
		board.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authenticatedAdmin{}, admin)))
	}))
	wrapped := httpmw.Middleware(logger)(mux)
	postFinished := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped.ServeHTTP(w, r)
		if r.Method == http.MethodPost && r.URL.Path == "/items" {
			postFinished <- struct{}{}
		}
	}))
	defer server.Close() // The HTTP server closes before the logger and pools.
	client := server.Client()
	client.Timeout = 5 * time.Second
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/items?token=query-secret", strings.NewReader(`{"name":"Published consumer","password":"password-secret","token":"request-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer header-secret")
	request.Header.Set("X-Request-ID", "consumer-release-request")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	wireBody, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusCreated || !strings.Contains(string(wireBody), "response-secret") {
		t.Fatalf("API response changed: status=%d read_error=%v", response.StatusCode, readErr)
	}
	select {
	case <-postFinished:
	case <-ctx.Done():
		t.Fatal("middleware did not complete")
	}
	if err = logger.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := store.List(ctx, apilog.Filter{})
	if err != nil || page.Total != 1 || len(page.Events) != 1 {
		t.Fatalf("expected one persisted event: total=%d count=%d error=%v", page.Total, len(page.Events), err)
	}
	event := page.Events[0]
	if event.Method != "POST" || event.Status != http.StatusCreated || event.RequestID != "consumer-release-request" {
		t.Fatal("request metadata was not preserved")
	}
	if event.Profile == nil || !event.Profile.Instrumented || event.Profile.Incomplete || event.Profile.QueryCount != 1 {
		t.Fatalf("expected one instrumented SQL query: %+v", event.Profile)
	}
	var requestBody, responseBody map[string]string
	if json.Unmarshal(event.Request.Data, &requestBody) != nil || json.Unmarshal(event.Response.Data, &responseBody) != nil {
		t.Fatal("complete JSON bodies were not captured")
	}
	if requestBody["name"] != "Published consumer" || requestBody["password"] != apilog.Filtered || requestBody["token"] != apilog.Filtered || responseBody["token"] != apilog.Filtered {
		t.Fatal("body redaction or safe values were not preserved")
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"query-secret", "password-secret", "request-secret", "response-secret", "header-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("a secret reached persistent storage")
		}
	}
	health := logger.Health().Outputs["database"]
	if health.Delivered != 1 || health.Failed != 0 || health.Dropped != 0 {
		t.Fatalf("unexpected delivery health: %+v", health)
	}
	for _, test := range []struct {
		path     string
		admin    bool
		status   int
		contains string
	}{
		{"/api-logs/", false, http.StatusForbidden, "Access denied"},
		{"/api-logs/", true, http.StatusOK, "/items"},
		{"/api-logs/events/" + event.ID, true, http.StatusOK, "Published consumer"},
		{"/api-logs/assets/dashboard.css", true, http.StatusOK, "{"},
	} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if test.admin {
			request.SetBasicAuth("admin", "consumer-test-password")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != test.status || !strings.Contains(string(body), test.contains) {
			t.Fatalf("dashboard route %s: status=%d read_error=%v", test.path, response.StatusCode, readErr)
		}
	}
	server.Close()
	if err = logger.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = store.List(ctx, apilog.Filter{})
	if err != nil || page.Total != 1 {
		t.Fatal("dashboard requests were logged recursively")
	}
	t.Log("Published modules captured and masked both bodies, profiled SQLite, delivered the event, denied anonymous dashboard access and rendered the authorized dashboard/detail/assets.")
}
