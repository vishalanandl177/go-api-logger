package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
)

func TestExampleWorkflow(t *testing.T) {
	ctx := context.Background()
	password := "test-only-long-password"
	directory := t.TempDir()
	if _, err := newDemo(ctx, options{Directory: directory, Password: password}); err == nil {
		t.Fatal("schema silently migrated")
	}
	d, err := newDemo(ctx, options{Directory: directory, Password: password, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := d.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	server := httptest.NewServer(d.Handler)
	defer server.Close()
	request, _ := http.NewRequest("POST", server.URL+"/items", strings.NewReader(`{"name":"Demo","token":"secret-not-in-logs"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatal(response.StatusCode)
	}
	for _, path := range []string{"/items", "/items/1", "/error", "/healthz"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	request, _ = http.NewRequest("GET", server.URL+"/api-logs/", nil)
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("dashboard unprotected")
	}
	request, _ = http.NewRequest("GET", server.URL+"/internal/metrics", nil)
	request.SetBasicAuth("admin", password)
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("metrics unavailable")
	}
	flush, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = d.Logger.Flush(flush); err != nil {
		t.Fatal(err)
	}
	page, err := d.Store.List(ctx, apilog.Filter{})
	if err != nil || page.Total != 4 {
		t.Fatalf("expected 4 API logs only: %+v %v", page, err)
	}
	var found bool
	for _, event := range page.Events {
		if event.Method == "POST" {
			found = true
			if strings.Contains(string(event.Request.Data), "secret-not-in-logs") {
				t.Fatal("secret persisted")
			}
			if event.Profile == nil || !event.Profile.Instrumented || event.Profile.QueryCount != 1 {
				t.Fatalf("missing SQL profile: %+v", event.Profile)
			}
		}
	}
	if !found {
		t.Fatal("missing create event")
	}
	request, _ = http.NewRequest("GET", server.URL+"/api-logs/", nil)
	request.SetBasicAuth("admin", password)
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), "/items") {
		t.Fatal("dashboard did not display persisted events")
	}
}
