package sql

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vishalanandl177/go-api-logger/integrations/internal/testutil"
)

type connectionFactory func() driver.Conn

func (f connectionFactory) Connect(context.Context) (driver.Conn, error) { return f(), nil }
func (f connectionFactory) Driver() driver.Driver                        { return fakeDriver{} }

type resettable struct{ *fakeConn }

func (*resettable) ResetSession(context.Context) error { return driver.ErrBadConn }

type validated struct{ *fakeConn }

func (*validated) IsValid() bool { return false }

type resetValidated struct{ *fakeConn }

func (*resetValidated) ResetSession(context.Context) error { return driver.ErrBadConn }
func (*resetValidated) IsValid() bool                      { return false }

func TestPoolInterfacesMatchUnderlyingDriver(t *testing.T) {
	for _, inner := range []driver.Conn{&fakeConn{}, &resettable{&fakeConn{}}, &validated{&fakeConn{}}, &resetValidated{&fakeConn{}}} {
		wrapped := wrapConn(inner)
		_, wantReset := inner.(driver.SessionResetter)
		reset, gotReset := wrapped.(driver.SessionResetter)
		_, wantValid := inner.(driver.Validator)
		valid, gotValid := wrapped.(driver.Validator)
		if wantReset != gotReset || wantValid != gotValid {
			t.Fatalf("%T: reset=%v/%v valid=%v/%v", inner, gotReset, wantReset, gotValid, wantValid)
		}
		if gotReset && reset.ResetSession(context.Background()) != driver.ErrBadConn {
			t.Fatal("reset was not delegated")
		}
		if gotValid && valid.IsValid() {
			t.Fatal("validation was not delegated")
		}
		if UnwrapConn(wrapped) != inner || wrapConn(wrapped) != wrapped {
			t.Fatal("wrapping must remain idempotent and reversible")
		}
	}
}

type closingConn struct {
	fakeConn
	closed chan struct{}
	once   sync.Once
}

func (c *closingConn) Close() error { c.once.Do(func() { close(c.closed) }); return nil }

func TestCanceledLegacyTransactionDiscardsConnection(t *testing.T) {
	inner := &closingConn{closed: make(chan struct{})}
	db := OpenDB(connectionFactory(func() driver.Conn { return inner }))
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := db.BeginTx(ctx, nil); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-inner.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("legacy connection was retained after transaction cancellation")
	}
}

// This type is accepted only by the prepared statement, never by the connection.
type statementValue struct{ value string }
type statementCheckerConn struct{ fakeConn }

func (*statementCheckerConn) Prepare(string) (driver.Stmt, error) { return &statementChecker{}, nil }

type statementChecker struct{ fakeStmt }

func (*statementChecker) CheckNamedValue(v *driver.NamedValue) error {
	if custom, ok := v.Value.(statementValue); ok {
		v.Value = custom.value
		return nil
	}
	return driver.ErrSkip
}

func TestPreparationPrecedesStatementArgumentConversion(t *testing.T) {
	ctx, finish := testutil.Exchange(t)
	db := OpenDB(connectionFactory(func() driver.Conn { return &statementCheckerConn{} }))
	defer db.Close()
	var got string
	if err := db.QueryRowContext(ctx, "SELECT ?", statementValue{"accepted"}).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "accepted" {
		t.Fatalf("got=%q", got)
	}
	if _, err := db.ExecContext(ctx, "UPDATE ?", statementValue{"accepted"}); err != nil {
		t.Fatal(err)
	}
	if e := finish(); e.Profile.QueryCount != 2 {
		t.Fatalf("profile=%+v", e.Profile)
	}
}

type variableStmtConn struct{ fakeConn }

func (*variableStmtConn) Prepare(string) (driver.Stmt, error) { return &variableStmt{}, nil }

type variableStmt struct{ fakeStmt }

func (*variableStmt) NumInput() int { return -1 }
func (*variableStmt) Exec(values []driver.Value) (driver.Result, error) {
	if _, ok := values[0].(int64); !ok {
		return nil, errors.New("argument did not use default conversion")
	}
	return driver.RowsAffected(1), nil
}

func TestVariableInputStatementUsesDefaultConversion(t *testing.T) {
	db := OpenDB(connectionFactory(func() driver.Conn { return &variableStmtConn{} }))
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), "UPDATE ?", int(42)); err != nil {
		t.Fatal(err)
	}
}

type skipFastConn struct{ fakeConn }

func (*skipFastConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, driver.ErrSkip
}
func (*skipFastConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, driver.ErrSkip
}

func TestExplicitErrSkipFallsBackWithoutCountingProbe(t *testing.T) {
	ctx, finish := testutil.Exchange(t)
	db := OpenDB(connectionFactory(func() driver.Conn { return &skipFastConn{} }))
	defer db.Close()
	var got string
	if err := db.QueryRowContext(ctx, "SELECT ?", "accepted").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE ?", "accepted"); err != nil {
		t.Fatal(err)
	}
	if e := finish(); e.Profile.QueryCount != 2 {
		t.Fatalf("fallback probe counted: %+v", e.Profile)
	}
}
