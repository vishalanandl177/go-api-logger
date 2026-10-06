// Package sql instruments database/sql through its driver and connector APIs.
// Install a wrapper when opening an application pool, not the API logger's pool.
package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"sync"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
)

// OpenDB creates a normal sql.DB using an instrumented connector. The caller
// owns the pool and closes it. Wrapping an already wrapped connector is a no-op.
func OpenDB(c driver.Connector) *sql.DB { return sql.OpenDB(WrapConnector(c)) }

func WrapConnector(c driver.Connector) driver.Connector {
	if _, ok := c.(*connector); ok {
		return c
	}
	return &connector{inner: c}
}

func WrapDriver(d driver.Driver) driver.Driver {
	if _, ok := d.(*wrappedDriver); ok {
		return d
	}
	return &wrappedDriver{inner: d}
}

type connector struct{ inner driver.Connector }

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return wrapConn(conn), nil
}
func (c *connector) Driver() driver.Driver { return WrapDriver(c.inner.Driver()) }

type wrappedDriver struct{ inner driver.Driver }

func (d *wrappedDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return wrapConn(c), nil
}
func (d *wrappedDriver) OpenConnector(name string) (driver.Connector, error) {
	if dc, ok := d.inner.(driver.DriverContext); ok {
		c, err := dc.OpenConnector(name)
		if err != nil {
			return nil, err
		}
		return WrapConnector(c), nil
	}
	return &legacyConnector{driver: d, name: name}, nil
}

type legacyConnector struct {
	driver *wrappedDriver
	name   string
}

func (c *legacyConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.driver.Open(c.name)
}
func (c *legacyConnector) Driver() driver.Driver { return c.driver }

type conn struct{ inner driver.Conn }

func wrapConn(c driver.Conn) driver.Conn {
	if _, ok := c.(interface{ apilogConn() driver.Conn }); ok {
		return c
	}
	return optionalConn(&conn{inner: c})
}

func (c *conn) apilogConn() driver.Conn { return c.inner }

// UnwrapConn supports driver-specific operations inside sql.Conn.Raw. Wrapping
// necessarily changes the concrete driver type exposed by Raw.
func UnwrapConn(c driver.Conn) driver.Conn {
	if w, ok := c.(interface{ apilogConn() driver.Conn }); ok {
		return w.apilogConn()
	}
	return c
}
func (c *conn) Close() error              { return c.inner.Close() }
func (c *conn) Begin() (driver.Tx, error) { return c.inner.Begin() }
func (c *conn) Prepare(query string) (driver.Stmt, error) {
	s, err := c.inner.Prepare(query)
	if err != nil {
		return nil, err
	}
	return newStmt(s, query, c.inner), nil
}
func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.inner.(driver.ConnPrepareContext); ok {
		s, err := p.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return newStmt(s, query, c.inner), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := c.Prepare(query)
	if err == nil && ctx.Err() != nil {
		s.Close()
		return nil, ctx.Err()
	}
	return s, err
}
func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.inner.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	if opts.Isolation != 0 {
		return nil, errors.New("apilog/sql: driver does not support non-default isolation")
	}
	if opts.ReadOnly {
		return nil, errors.New("apilog/sql: driver does not support read-only transactions")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := c.inner.Begin()
	if err == nil && ctx.Err() != nil {
		tx.Rollback()
		return nil, ctx.Err()
	}
	return tx, err
}
func (c *conn) Ping(ctx context.Context) error {
	if p, ok := c.inner.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}
func (c *conn) CheckNamedValue(v *driver.NamedValue) error {
	if n, ok := c.inner.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(v)
	}
	return driver.ErrSkip
}
func values(named []driver.NamedValue) ([]driver.Value, error) {
	v := make([]driver.Value, len(named))
	for i, n := range named {
		if n.Name != "" {
			return nil, errors.New("apilog/sql: legacy driver does not support named parameters")
		}
		v[i] = n.Value
	}
	return v, nil
}
func (c *conn) execContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	start := time.Now()
	var result driver.Result
	var err error
	if e, ok := c.inner.(driver.ExecerContext); ok {
		result, err = e.ExecContext(apilog.SuppressProfiling(ctx), query, args)
	} else if e, ok := c.inner.(driver.Execer); ok {
		var v []driver.Value
		if v, err = values(args); err == nil {
			if err = ctx.Err(); err == nil {
				result, err = e.Exec(query, v)
			}
		}
	} else {
		return nil, driver.ErrSkip
	}
	if err != driver.ErrSkip {
		apilog.RecordQuery(ctx, "database/sql", query, start, err)
	}
	return result, err
}
func (c *conn) queryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	start := time.Now()
	var result driver.Rows
	var err error
	if q, ok := c.inner.(driver.QueryerContext); ok {
		result, err = q.QueryContext(apilog.SuppressProfiling(ctx), query, args)
	} else if q, ok := c.inner.(driver.Queryer); ok {
		var v []driver.Value
		if v, err = values(args); err == nil {
			if err = ctx.Err(); err == nil {
				result, err = q.Query(query, v)
			}
		}
	} else {
		return nil, driver.ErrSkip
	}
	return queryResult(ctx, query, start, result, err)
}

type stmt struct {
	inner       driver.Stmt
	query       string
	connChecker driver.NamedValueChecker
}

func newStmt(s driver.Stmt, query string, c driver.Conn) driver.Stmt {
	n, _ := c.(driver.NamedValueChecker)
	w := &stmt{inner: s, query: query, connChecker: n}
	if converter, ok := s.(driver.ColumnConverter); ok {
		return &struct {
			*stmt
			driver.ColumnConverter
		}{w, converter}
	}
	return w
}
func (s *stmt) Close() error                                    { return s.inner.Close() }
func (s *stmt) NumInput() int                                   { return s.inner.NumInput() }
func (s *stmt) Exec(args []driver.Value) (driver.Result, error) { return s.inner.Exec(args) }
func (s *stmt) Query(args []driver.Value) (driver.Rows, error)  { return s.inner.Query(args) }
func (s *stmt) CheckNamedValue(v *driver.NamedValue) error {
	if n, ok := s.inner.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(v)
	}
	if s.connChecker != nil {
		return s.connChecker.CheckNamedValue(v)
	}
	return driver.ErrSkip
}
func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	start := time.Now()
	var result driver.Result
	var err error
	if e, ok := s.inner.(driver.StmtExecContext); ok {
		result, err = e.ExecContext(apilog.SuppressProfiling(ctx), args)
	} else {
		var v []driver.Value
		if v, err = values(args); err == nil {
			if err = ctx.Err(); err == nil {
				result, err = s.inner.Exec(v)
			}
		}
	}
	apilog.RecordQuery(ctx, "database/sql", s.query, start, err)
	return result, err
}
func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	start := time.Now()
	var result driver.Rows
	var err error
	if q, ok := s.inner.(driver.StmtQueryContext); ok {
		result, err = q.QueryContext(apilog.SuppressProfiling(ctx), args)
	} else {
		var v []driver.Value
		if v, err = values(args); err == nil {
			if err = ctx.Err(); err == nil {
				result, err = s.inner.Query(v)
			}
		}
	}
	return queryResult(ctx, s.query, start, result, err)
}
func queryResult(ctx context.Context, query string, start time.Time, result driver.Rows, err error) (driver.Rows, error) {
	if err == driver.ErrSkip {
		return result, err
	}
	end := apilog.StartQueryAt(ctx, "database/sql", query, start)
	if err != nil {
		end(err)
		return nil, err
	}
	if result == nil {
		end(nil)
		return result, nil
	}
	return &rows{inner: result, end: end}, nil
}

type rows struct {
	inner driver.Rows
	end   func(error)
	once  sync.Once
}

func (r *rows) finish(err error)  { r.once.Do(func() { r.end(err) }) }
func (r *rows) Columns() []string { return r.inner.Columns() }
func (r *rows) Close() error      { err := r.inner.Close(); r.finish(err); return err }
func (r *rows) Next(dest []driver.Value) error {
	err := r.inner.Next(dest)
	if err != nil {
		if err == io.EOF {
			if !r.HasNextResultSet() {
				r.finish(nil)
			}
		} else {
			r.finish(err)
		}
	}
	return err
}
func (r *rows) HasNextResultSet() bool {
	if n, ok := r.inner.(driver.RowsNextResultSet); ok {
		return n.HasNextResultSet()
	}
	return false
}
func (r *rows) NextResultSet() error {
	if n, ok := r.inner.(driver.RowsNextResultSet); ok {
		err := n.NextResultSet()
		if err == io.EOF {
			r.finish(nil)
		} else if err != nil {
			r.finish(err)
		}
		return err
	}
	r.finish(nil)
	return io.EOF
}
func (r *rows) ColumnTypeDatabaseTypeName(i int) string {
	if m, ok := r.inner.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return m.ColumnTypeDatabaseTypeName(i)
	}
	return ""
}
func (r *rows) ColumnTypeLength(i int) (int64, bool) {
	if m, ok := r.inner.(driver.RowsColumnTypeLength); ok {
		return m.ColumnTypeLength(i)
	}
	return 0, false
}
func (r *rows) ColumnTypeNullable(i int) (bool, bool) {
	if m, ok := r.inner.(driver.RowsColumnTypeNullable); ok {
		return m.ColumnTypeNullable(i)
	}
	return false, false
}
func (r *rows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	if m, ok := r.inner.(driver.RowsColumnTypePrecisionScale); ok {
		return m.ColumnTypePrecisionScale(i)
	}
	return 0, 0, false
}
func (r *rows) ColumnTypeScanType(i int) reflect.Type {
	if m, ok := r.inner.(driver.RowsColumnTypeScanType); ok {
		return m.ColumnTypeScanType(i)
	}
	return reflect.TypeFor[any]()
}
