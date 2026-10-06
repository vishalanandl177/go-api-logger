package sql

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"errors"
	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/integrations/internal/testutil"
	"io"
	"modernc.org/sqlite"
	"testing"
)

func TestSQLitePreparedTransactionsRows(t *testing.T) {
	ctx, finish := testutil.Exchange(t)
	d := WrapDriver(&sqlite.Driver{}).(driver.DriverContext)
	c, err := d.OpenConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db := OpenDB(c)
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "CREATE TABLE users (id INTEGER, secret TEXT)"); err != nil {
		t.Fatal(err)
	}
	s, err := db.PrepareContext(ctx, "INSERT INTO users VALUES (?, ?)")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.ExecContext(ctx, 1, "never store me"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO users VALUES (?, ?)", 2, "also secret"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count=%d", count)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.ExecContext(canceled, "DELETE FROM users"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	e := finish()
	if e.Profile.QueryCount != 4 || !e.Profile.Instrumented || e.Profile.Incomplete {
		t.Fatalf("profile=%+v", e.Profile)
	}
}

func TestLegacyFallbackAndConnectionNamedValueChecker(t *testing.T) {
	ctx, finish := testutil.Exchange(t)
	db := stdsql.OpenDB(WrapConnector(fakeConnector{}))
	defer db.Close()
	var got string
	if err := db.QueryRowContext(ctx, "SELECT ?", customValue("answer")).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "answer" {
		t.Fatalf("got=%q", got)
	}
	if _, err := db.ExecContext(ctx, "UPDATE ?", customValue("updated")); err != nil {
		t.Fatal(err)
	}
	e := finish()
	if e.Profile.QueryCount != 2 {
		t.Fatalf("fallback probe was counted: %+v", e.Profile)
	}
}

func TestRowsRemainActiveUntilClose(t *testing.T) {
	ctx, finish := testutil.Exchange(t)
	db := stdsql.OpenDB(WrapConnector(fakeConnector{}))
	defer db.Close()
	r, err := db.QueryContext(ctx, "SELECT ?", customValue("x"))
	if err != nil {
		t.Fatal(err)
	}
	e := finish()
	if !e.Profile.Incomplete || e.Profile.QueryCount != 1 {
		t.Fatalf("profile=%+v", e.Profile)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

type customValue string
type fakeConnector struct{}

func (fakeConnector) Connect(context.Context) (driver.Conn, error) { return &fakeConn{}, nil }
func (fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct{}

func (*fakeConn) Prepare(string) (driver.Stmt, error) { return &fakeStmt{}, nil }
func (*fakeConn) Close() error                        { return nil }
func (*fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }
func (*fakeConn) CheckNamedValue(v *driver.NamedValue) error {
	if s, ok := v.Value.(customValue); ok {
		v.Value = string(s)
		return nil
	}
	return driver.ErrSkip
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeStmt struct{}

func (*fakeStmt) Close() error                                { return nil }
func (*fakeStmt) NumInput() int                               { return 1 }
func (*fakeStmt) Exec([]driver.Value) (driver.Result, error)  { return driver.RowsAffected(1), nil }
func (*fakeStmt) Query(v []driver.Value) (driver.Rows, error) { return &fakeRows{value: v[0]}, nil }

type fakeRows struct {
	value driver.Value
	read  bool
}

type instrumentedConnector struct{}

func (instrumentedConnector) Connect(context.Context) (driver.Conn, error) {
	return &instrumentedConn{}, nil
}
func (instrumentedConnector) Driver() driver.Driver { return fakeDriver{} }

type instrumentedConn struct{ fakeConn }

func (*instrumentedConn) ExecContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	end := apilog.StartQuery(ctx, "native-driver", q)
	defer end(nil)
	return driver.RowsAffected(1), nil
}
func (*instrumentedConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	end := apilog.StartQuery(ctx, "native-driver", q)
	defer end(nil)
	return &fakeRows{value: "value"}, nil
}
func TestSuppressesNestedNativeInstrumentation(t *testing.T) {
	ctx, finish := testutil.Exchange(t)
	db := OpenDB(instrumentedConnector{})
	defer db.Close()
	if _, err := db.ExecContext(ctx, "UPDATE items SET id = 1"); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRowContext(ctx, "SELECT name FROM items").Scan(&got); err != nil {
		t.Fatal(err)
	}
	e := finish()
	if e.Profile.QueryCount != 2 {
		t.Fatalf("nested instrumentation counted twice: %+v", e.Profile)
	}
}

func (*fakeRows) Columns() []string { return []string{"answer"} }
func (*fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(v []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	v[0] = r.value
	return nil
}
