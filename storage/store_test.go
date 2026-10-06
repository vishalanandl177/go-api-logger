package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
)

func TestSQLStores(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		t.Run(string(dialect), func(t *testing.T) {
			ctx := context.Background()
			dsn := os.Getenv("API_LOGGER_TEST_" + strings.ToUpper(string(dialect)) + "_DSN")
			if dialect != SQLite && dsn == "" {
				t.Skip("set dedicated empty test database DSN to run")
			}
			if dialect == SQLite {
				dsn = filepath.Join(t.TempDir(), "events.db")
			}
			driver := string(dialect)
			if dialect == Postgres {
				driver = "pgx"
			}
			s, db, err := open(ctx, driver, dsn, dialect)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if err = s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if err = s.Migrate(ctx); err != nil {
				t.Fatalf("idempotent migration: %v", err)
			}
			if err = s.Check(ctx); err != nil {
				t.Fatal(err)
			}
			initial, err := s.List(ctx, apilog.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if initial.Total != 0 {
				t.Fatal("integration tests require an empty dedicated database")
			}
			base := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)
			events := []apilog.Event{
				{Version: 1, ID: "a", Time: base, Duration: 100 * time.Millisecond, Method: "GET", URL: "/alpha?q=100%_done", Path: "/alpha", Status: 200, Request: apilog.Body{Data: json.RawMessage(`{"nested":"needle","token":"***FILTERED***"}`), State: "captured"}, Profile: &apilog.Profile{Instrumented: true, QueryCount: 0}},
				{Version: 1, ID: "b", Time: base.Add(time.Hour), Duration: 300 * time.Millisecond, Method: "POST", URL: "/beta", Status: 201, RequestHeaders: map[string][]string{"X-Test": {"NéEdLe"}}, Profile: &apilog.Profile{Instrumented: true, QueryCount: 6}},
				{Version: 1, ID: "c", Time: base.Add(2 * time.Hour), Duration: 900 * time.Millisecond, Method: "GET", URL: "/gamma", Status: 500, Profile: &apilog.Profile{Instrumented: true, QueryCount: 12}},
				{Version: 1, ID: "d", Time: base.Add(48 * time.Hour), Duration: 50 * time.Millisecond, Method: "GET", URL: "/delta", Status: 404, Profile: &apilog.Profile{Instrumented: false}},
			}
			t.Cleanup(func() { _, _ = s.Delete(context.Background(), []string{"a", "b", "c", "d", "new", "parallel"}) })
			if err = s.WriteBatch(ctx, events); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, "a")
			if err != nil || got.ID != "a" || string(got.Request.Data) != string(events[0].Request.Data) || !got.Time.Equal(base) {
				t.Fatalf("round trip: %+v %v", got, err)
			}
			if _, err = s.Get(ctx, "absent"); !errors.Is(err, apilog.ErrNotFound) {
				t.Fatalf("missing: %v", err)
			}
			truth, falsity := true, false
			zero, four, five, nine, ten := 0, 4, 5, 9, 10
			cases := []struct {
				name   string
				filter apilog.Filter
				want   int64
			}{
				{"all", apilog.Filter{}, 4}, {"method", apilog.Filter{Methods: []string{"POST"}}, 1}, {"statuses", apilog.Filter{StatusCodes: []int{404, 500}}, 2},
				{"search", apilog.Filter{Search: "NEEDLE"}, 1}, {"unicode", apilog.Filter{Search: "néedle"}, 1}, {"literal wildcard", apilog.Filter{Search: "100%_done"}, 1}, {"injection", apilog.Filter{Search: "' OR 1=1 --"}, 0},
				{"time", apilog.Filter{After: base.Add(time.Hour), Before: base.Add(2 * time.Hour)}, 1}, {"slow", apilog.Filter{Slow: &truth, SlowThreshold: 200 * time.Millisecond}, 2}, {"fast", apilog.Filter{Slow: &falsity, SlowThreshold: 200 * time.Millisecond}, 2},
				{"low sql", apilog.Filter{SQLMin: &zero, SQLMax: &four}, 1}, {"medium sql", apilog.Filter{SQLMin: &five, SQLMax: &nine}, 1}, {"high sql", apilog.Filter{SQLMin: &ten}, 1}, {"unprofiled", apilog.Filter{Profiled: &falsity}, 1}, {"profiled", apilog.Filter{Profiled: &truth}, 3}, {"ids", apilog.Filter{IDs: []string{"a", "d"}}, 2},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					page, err := s.List(ctx, test.filter)
					if err != nil || page.Total != test.want || int64(len(page.Events)) != test.want {
						t.Fatalf("got %+v %v want %d", page, err, test.want)
					}
					stats, err := s.Aggregate(ctx, test.filter)
					if err != nil {
						t.Fatal(err)
					}
					var count int64
					for _, d := range stats.Days {
						count += d.Count
					}
					if count != test.want {
						t.Fatalf("aggregate %d != list %d", count, test.want)
					}
				})
			}
			page, err := s.List(ctx, apilog.Filter{Order: "duration", Limit: 2, Page: 2})
			if err != nil || page.Events[0].ID != "b" || page.Events[1].ID != "c" || page.Total != 4 {
				t.Fatalf("pagination: %+v %v", page, err)
			}
			sqlPage, err := s.List(ctx, apilog.Filter{Order: "sql"})
			if err != nil || len(sqlPage.Events) != 4 || sqlPage.Events[0].ID != "a" || sqlPage.Events[3].ID != "d" {
				t.Fatalf("SQL ordering: %+v %v", sqlPage, err)
			}
			stats, err := s.Aggregate(ctx, apilog.Filter{})
			if err != nil || len(stats.Days) != 3 || stats.Days[1].Count != 2 || stats.Days[1].AverageSQL != 9 || len(stats.Statuses) != 4 {
				t.Fatalf("charts: %+v %v", stats, err)
			}
			for _, filter := range []apilog.Filter{{Order: "status; DROP TABLE api_logger_events"}, {Limit: 501}, {Page: -1}, {Slow: &truth}, {SQLMin: &ten, SQLMax: &zero}, {Before: base, After: base.Add(time.Hour)}} {
				if _, err = s.List(ctx, filter); !errors.Is(err, ErrInvalid) {
					t.Fatalf("invalid filter allowed: %+v %v", filter, err)
				}
			}
			newEvent := events[0]
			newEvent.ID = "new"
			if err = s.WriteBatch(ctx, []apilog.Event{newEvent, events[0]}); err == nil {
				t.Fatal("duplicate batch accepted")
			}
			if _, err = s.Get(ctx, "new"); !errors.Is(err, apilog.ErrNotFound) {
				t.Fatal("batch was not rolled back")
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err = s.WriteBatch(canceled, []apilog.Event{newEvent}); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			pruned, err := s.Prune(ctx, apilog.PruneOptions{Before: base.Add(2 * time.Hour), DryRun: true, BatchSize: 1})
			if err != nil || pruned.Matched != 2 || pruned.Deleted != 0 {
				t.Fatalf("dryrun %+v %v", pruned, err)
			}
			page, _ = s.List(ctx, apilog.Filter{})
			if page.Total != 4 {
				t.Fatal("dry run deleted events")
			}
			pruned, err = s.Prune(ctx, apilog.PruneOptions{Before: base.Add(2 * time.Hour), BatchSize: 1})
			if err != nil || pruned.Deleted != 2 {
				t.Fatalf("prune %+v %v", pruned, err)
			}
			deleted, err := s.Delete(ctx, []string{"c", "d"})
			if err != nil || deleted != 2 {
				t.Fatalf("delete %d %v", deleted, err)
			}
			if deleted, err = s.Delete(ctx, nil); err != nil || deleted != 0 {
				t.Fatal("empty delete")
			}
			if db.PingContext(ctx) != nil {
				t.Fatal("store closed caller database")
			}
		})
	}
}

func TestSQLiteConcurrentAndSchema(t *testing.T) {
	ctx := context.Background()
	s, db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "parallel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !errors.Is(s.Check(ctx), ErrSchema) {
		t.Fatal("missing schema must be diagnosed")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event := apilog.Event{Version: 1, ID: fmt.Sprint(i), Time: time.Now(), Method: "GET", Status: 200}
			if err := s.WriteBatch(ctx, []apilog.Event{event}); err != nil {
				t.Error(err)
			}
			if _, err := s.List(ctx, apilog.Filter{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	page, err := s.List(ctx, apilog.Filter{})
	if err != nil || page.Total != 20 {
		t.Fatalf("concurrent %+v %v", page, err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO api_logger_schema (version) VALUES (999)"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Check(ctx), ErrSchema) || !errors.Is(s.Migrate(ctx), ErrSchema) {
		t.Fatal("future version accepted")
	}
}

func TestSafeFailuresAndValidation(t *testing.T) {
	if _, err := New(nil, SQLite); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, db, err := OpenSQLite(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secret := "password=do-not-report"
	event := apilog.Event{Version: 1, ID: secret, Time: time.Now(), Method: "GET", Status: 200}
	if err = s.WriteBatch(ctx, []apilog.Event{event}); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe error: %v", err)
	}
	event.Request.Data = json.RawMessage("invalid json")
	if err = s.WriteBatch(ctx, []apilog.Event{event}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid event accepted")
	}
	if _, err = s.Prune(ctx, apilog.PruneOptions{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded prune allowed")
	}
}

func TestMigrationDetectsMissingTable(t *testing.T) {
	ctx := context.Background()
	s, db, err := OpenSQLite(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE api_logger_events"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Migrate(ctx), ErrSchema) {
		t.Fatal("migrate claimed success for damaged versioned schema")
	}
}
