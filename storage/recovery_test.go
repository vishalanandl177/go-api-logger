package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
	"modernc.org/sqlite"
)

// A real SQLite write lock represents an unavailable output without stopping
// the API or the independent subscriber. Recovery requires no logger restart.
func TestLoggerRecoversAfterSQLiteWriteLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	filename := filepath.Join(t.TempDir(), "recovery.db")
	store, db, err := OpenSQLite(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	blocker, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	lock, err := blocker.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer lock.ExecContext(context.Background(), "ROLLBACK")

	var mu sync.Mutex
	var exported []apilog.Event
	cfg := apilog.DefaultConfig()
	cfg.Queue.BatchSize = 1
	cfg.Queue.WriteTimeout = 200 * time.Millisecond
	cfg.Queue.FlushInterval = time.Hour
	cfg.Outputs = []apilog.Output{
		{Name: "database", Kind: "storage", Sink: store},
		{Name: "subscriber", Kind: "export", Sink: apilog.SinkFunc(func(_ context.Context, events []apilog.Event) error {
			mu.Lock()
			defer mu.Unlock()
			exported = append(exported, events...)
			return nil
		})},
	}
	logger, err := apilog.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if err := logger.Shutdown(shutdown); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	handler := httpmw.Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	request := func(path string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusCreated || response.Body.String() != `{"status":"accepted"}` {
			t.Fatalf("logging changed the application response: %d %q", response.Code, response.Body.String())
		}
		if err := logger.Flush(ctx); err != nil {
			t.Fatalf("batch did not settle: %v", err)
		}
	}

	request("/during-outage")
	health := logger.Health()
	if got := health.Outputs["database"]; got.Accepted != 1 || got.Failed != 1 || got.Delivered != 0 || !got.WorkerRunning {
		t.Fatalf("database failure was not isolated/accounted: %+v", got)
	}
	if got := health.Outputs["subscriber"]; got.Delivered != 1 || got.Failed != 0 {
		t.Fatalf("database failure affected subscriber: %+v", got)
	}
	if _, err := lock.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}

	request("/after-recovery")
	health = logger.Health()
	if got := health.Outputs["database"]; got.Accepted != 2 || got.Delivered != 1 || got.Failed != 1 || got.Dropped != 0 || got.Queued != 0 || got.InFlight != 0 || !got.WorkerRunning {
		t.Fatalf("logger did not recover on the next batch: %+v", got)
	}
	page, err := store.List(ctx, apilog.Filter{})
	if err != nil || page.Total != 1 || len(page.Events) != 1 || page.Events[0].Path != "/after-recovery" {
		t.Fatalf("failed batch was replayed or recovered batch was lost: %+v %v", page, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(exported) != 2 || exported[0].Path != "/during-outage" || exported[1].Path != "/after-recovery" {
		t.Fatalf("subscriber did not receive both requests: %+v", exported)
	}
}

// SQLite accepts both ? and $n placeholders. Wrapping its real driver lets us
// exercise each SQLStore binding path with controlled transaction faults, without
// pretending these are live PostgreSQL/MySQL connection-recovery tests.
func TestSQLStoreNextBatchAfterTransactionFailure(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, stage := range []string{"begin", "prepare", "exec", "commit_ack"} {
			t.Run(string(dialect)+"/"+stage, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				connector, err := sqlite.NewConnector(filepath.Join(t.TempDir(), "fault.db"))
				if err != nil {
					t.Fatal(err)
				}
				fault := &transactionFault{}
				db := sql.OpenDB(&faultConnector{Connector: connector, fault: fault})
				db.SetMaxOpenConns(1)
				defer db.Close()
				schema, err := NewSQLite(db)
				if err != nil {
					t.Fatal(err)
				}
				if err := schema.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
				store, err := New(db, dialect)
				if err != nil {
					t.Fatal(err)
				}
				fault.arm(stage)
				events := []apilog.Event{recoveryEvent("first"), recoveryEvent("second")}
				err = store.WriteBatch(ctx, events)
				var safe *Error
				if !errors.As(err, &safe) || safe.Operation != "write" || strings.Contains(err.Error(), "private-driver-detail") {
					t.Fatalf("driver failure not returned safely: %v", err)
				}
				if !fault.fired {
					t.Fatal("injected fault was not exercised")
				}
				// Losing a commit acknowledgement is ambiguous: the rows can exist
				// even though the sink returned an error. Never replay automatically.
				wantFailedRows := int64(0)
				if stage == "commit_ack" {
					wantFailedRows = 2
				}
				page, err := store.List(ctx, apilog.Filter{})
				if err != nil || page.Total != wantFailedRows {
					t.Fatalf("transaction failure state: %+v %v, want %d rows", page, err, wantFailedRows)
				}
				if err := store.WriteBatch(ctx, []apilog.Event{recoveryEvent("recovered")}); err != nil {
					t.Fatalf("next batch on same store/pool did not recover: %v", err)
				}
				page, err = store.List(ctx, apilog.Filter{})
				if err != nil || page.Total != wantFailedRows+1 {
					t.Fatalf("next batch persistence: %+v %v", page, err)
				}
				if db.Stats().InUse != 0 {
					t.Fatal("failed transaction leaked a pool connection")
				}
				if err := db.PingContext(ctx); err != nil {
					t.Fatalf("store closed caller-owned pool: %v", err)
				}
			})
		}
	}
}

func recoveryEvent(id string) apilog.Event {
	return apilog.Event{Version: apilog.SchemaVersion, ID: id, Time: time.Now(), Method: http.MethodGet, Status: http.StatusOK}
}

// Faults are armed only between synchronous database operations in this test.
type transactionFault struct {
	stage string
	seen  int
	fired bool
}

func (f *transactionFault) arm(stage string) { f.stage, f.seen, f.fired = stage, 0, false }
func (f *transactionFault) check(stage string) error {
	if f.stage != stage || f.fired {
		return nil
	}
	f.seen++
	if stage == "exec" && f.seen < 2 { // Fail after one row was inserted.
		return nil
	}
	f.fired = true
	return fmt.Errorf("temporary %s failure: private-driver-detail", stage)
}

type faultConnector struct {
	driver.Connector
	fault *transactionFault
}

func (c *faultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &faultConnection{Conn: conn, fault: c.fault}, nil
}

type faultConnection struct {
	driver.Conn
	fault *transactionFault
}

func (c *faultConnection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if err := c.fault.check("begin"); err != nil {
		return nil, err
	}
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &faultTransaction{Tx: tx, fault: c.fault}, nil
}

func (c *faultConnection) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := c.fault.check("prepare"); err != nil {
		return nil, err
	}
	stmt, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &faultStatement{Stmt: stmt, fault: c.fault}, nil
}

type faultTransaction struct {
	driver.Tx
	fault *transactionFault
}

func (tx *faultTransaction) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	return tx.fault.check("commit_ack")
}

type faultStatement struct {
	driver.Stmt
	fault *transactionFault
}

func (s *faultStatement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.fault.check("exec"); err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}
