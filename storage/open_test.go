package storage

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
)

func TestOpenSQLiteReadOnly(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	filename := filepath.Join(directory, "read only #percent%.db")
	store, db, err := OpenSQLite(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	event := apilog.Event{ID: "old", Time: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Method: "GET", Status: 200}
	if err = store.WriteBatch(ctx, []apilog.Event{event}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	plainURI, err := sqliteReadOnlyDSN(filename)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(plainURI)
	if err != nil {
		t.Fatal(err)
	}
	parsed.RawQuery = "mode=rwc&cache=private"
	for _, dsn := range []string{filename, plainURI, parsed.String()} {
		store, db, err := OpenSQLiteReadOnly(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if err = store.Check(ctx); err != nil {
			t.Fatal(err)
		}
		page, err := store.List(ctx, apilog.Filter{})
		if err != nil || page.Total != 1 {
			t.Fatalf("read-only list failed: %+v %v", page, err)
		}
		preview, err := store.Prune(ctx, apilog.PruneOptions{Before: time.Now(), DryRun: true})
		if err != nil || preview.Matched != 1 || preview.Deleted != 0 {
			t.Fatalf("read-only preview failed: %+v %v", preview, err)
		}
		if _, err = db.ExecContext(ctx, "CREATE TABLE unexpected_write(id INTEGER)"); err == nil {
			t.Fatal("read-only connection allowed writing")
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only operations changed database bytes")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatal("read-only operations created files")
	}
}

func TestOpenSQLiteReadOnlyMissingAndInvalid(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	missing := filepath.Join(directory, "absent.db")
	uri, err := sqliteReadOnlyDSN(missing)
	if err != nil {
		t.Fatal(err)
	}
	for _, dsn := range []string{missing, uri, strings.Replace(uri, "mode=ro", "mode=rwc", 1), filepath.Join(directory, "absent-directory", "absent.db")} {
		if _, db, err := OpenSQLiteReadOnly(ctx, dsn); err == nil {
			db.Close()
			t.Fatal("opened missing database")
		}
	}
	for _, dsn := range []string{
		"", ":memory:", "file::memory:", "file:%3Amemory%3A", "file:", "file://elsewhere/database", "file:database#fragment",
		missing + "?mode=memory", missing + "?mode=ro&mode=rwc", missing + "?cache=invalid", missing + "?cache=private&cache=shared",
		missing + "?_pragma=journal_mode(WAL)", missing + "?_pragma=user_version%3D77", missing + "?%5Fpragma=user_version%3D77",
		missing + "?_journal_mode=WAL", missing + "?_auto_vacuum=FULL", missing + "?vfs=memdb", missing + "?immutable=1", missing + "?nolock=1",
		missing + "?mode=%zz", "file:database%00.db", "database\x00.db",
	} {
		if _, db, err := OpenSQLiteReadOnly(ctx, dsn); err == nil {
			db.Close()
			t.Fatal("unsafe or invalid DSN accepted")
		} else if strings.Contains(err.Error(), dsn) && dsn != "" {
			t.Fatal("DSN leaked in error")
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed read-only opens created filesystem entries")
	}
}
