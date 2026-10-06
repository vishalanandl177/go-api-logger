package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/storage"
)

func TestCommands(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "commands.db")
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	invoke := func(args ...string) (int, report, string) {
		var out, errs bytes.Buffer
		code := run(args, &out, &errs, func(string) string { return dsn }, func() time.Time { return now })
		var result report
		_ = json.Unmarshal(out.Bytes(), &result)
		return code, result, out.String() + errs.String()
	}
	if code, _, _ := invoke("doctor", "--format", "json"); code != 1 {
		t.Fatal("missing schema should fail")
	}
	if code, result, msg := invoke("migrate", "--format", "json"); code != 0 || result.SchemaVersion != 1 {
		t.Fatalf("migrate %d %+v %s", code, result, msg)
	}
	if code, _, msg := invoke("doctor", "--format", "json"); code != 0 {
		t.Fatal(msg)
	}
	if code, _, _ := invoke("doctor", "--format", "json", "--fail-level", "warning"); code != 1 {
		t.Fatal("warning threshold ignored")
	}
	store, db, err := storage.OpenSQLite(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WriteBatch(context.Background(), []apilog.Event{{Version: 1, ID: "old", Time: now.AddDate(0, 0, -31), Method: "GET", Status: 200}, {Version: 1, ID: "recent", Time: now, Method: "GET", Status: 200}}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if code, result, msg := invoke("prune", "--days", "30", "--dry-run", "--format", "json"); code != 0 || result.Matched != 1 || result.Deleted != 0 || !result.DryRun {
		t.Fatalf("dryrun %d %+v %s", code, result, msg)
	}
	if code, result, msg := invoke("prune", "--before", "2026-04-01", "--batch-size", "1", "--format", "json"); code != 0 || result.Deleted != 1 {
		t.Fatalf("prune %d %+v %s", code, result, msg)
	}
	for _, args := range [][]string{{"prune"}, {"prune", "--days", "-1"}, {"prune", "--days", "30", "--before", "2026-01-01"}, {"prune", "--before", "bad"}, {"doctor", "--format", "xml"}, {"migrate", "--driver", "injected"}, {"doctor", "--timeout", "0s"}, {"migrate", "--dry-run"}, {"prune", "--days", "0", "--before", "2026-01-01"}} {
		if code, _, _ := invoke(args...); code != 2 {
			t.Fatalf("invalid options accepted: %v", args)
		}
	}
}

func TestDoesNotExposeDSNOrFlagValues(t *testing.T) {
	secret := "postgres://person:do-not-expose@invalid.invalid/db"
	var out, errs bytes.Buffer
	code := run([]string{"doctor", "--driver", "postgres", "--timeout", "1ms", "--format", "json"}, &out, &errs, func(string) string { return secret }, time.Now)
	if code != 1 || strings.Contains(out.String()+errs.String(), "do-not-expose") {
		t.Fatalf("unsafe failure %s %s", &out, &errs)
	}
	out.Reset()
	errs.Reset()
	code = run([]string{"doctor", "--timeout", secret}, &out, &errs, func(string) string { return "" }, time.Now)
	if code != 2 || strings.Contains(out.String()+errs.String(), secret) {
		t.Fatal("unsafe parser error")
	}
}
