// Package-level interface combinations preserve database/sql dispatch behavior.
package sql

import (
	"context"
	"database/sql/driver"
)

type contextExecer struct{ c *conn }

func (e contextExecer) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return e.c.execContext(ctx, query, args)
}

type contextQueryer struct{ c *conn }

func (q contextQueryer) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return q.c.queryContext(ctx, query, args)
}

// The fast-path interfaces determine when database/sql converts arguments. If
// absent, preparation must happen first so a statement's NamedValueChecker can
// handle custom types. SessionResetter and Validator jointly determine whether
// a canceled transaction's connection may safely return to the pool.
func optionalConn(c *conn) driver.Conn {
	mask := 0
	_, execContext := c.inner.(driver.ExecerContext)
	_, execLegacy := c.inner.(driver.Execer)
	if execContext || execLegacy {
		mask |= 1
	}
	_, queryContext := c.inner.(driver.QueryerContext)
	_, queryLegacy := c.inner.(driver.Queryer)
	if queryContext || queryLegacy {
		mask |= 2
	}
	reset, hasReset := c.inner.(driver.SessionResetter)
	if hasReset {
		mask |= 4
	}
	valid, hasValid := c.inner.(driver.Validator)
	if hasValid {
		mask |= 8
	}
	switch mask {
	case 1:
		return &struct {
			*conn
			driver.ExecerContext
		}{c, contextExecer{c}}
	case 2:
		return &struct {
			*conn
			driver.QueryerContext
		}{c, contextQueryer{c}}
	case 3:
		return &struct {
			*conn
			driver.ExecerContext
			driver.QueryerContext
		}{c, contextExecer{c}, contextQueryer{c}}
	case 4:
		return &struct {
			*conn
			driver.SessionResetter
		}{c, reset}
	case 5:
		return &struct {
			*conn
			driver.ExecerContext
			driver.SessionResetter
		}{c, contextExecer{c}, reset}
	case 6:
		return &struct {
			*conn
			driver.QueryerContext
			driver.SessionResetter
		}{c, contextQueryer{c}, reset}
	case 7:
		return &struct {
			*conn
			driver.ExecerContext
			driver.QueryerContext
			driver.SessionResetter
		}{c, contextExecer{c}, contextQueryer{c}, reset}
	case 8:
		return &struct {
			*conn
			driver.Validator
		}{c, valid}
	case 9:
		return &struct {
			*conn
			driver.ExecerContext
			driver.Validator
		}{c, contextExecer{c}, valid}
	case 10:
		return &struct {
			*conn
			driver.QueryerContext
			driver.Validator
		}{c, contextQueryer{c}, valid}
	case 11:
		return &struct {
			*conn
			driver.ExecerContext
			driver.QueryerContext
			driver.Validator
		}{c, contextExecer{c}, contextQueryer{c}, valid}
	case 12:
		return &struct {
			*conn
			driver.SessionResetter
			driver.Validator
		}{c, reset, valid}
	case 13:
		return &struct {
			*conn
			driver.ExecerContext
			driver.SessionResetter
			driver.Validator
		}{c, contextExecer{c}, reset, valid}
	case 14:
		return &struct {
			*conn
			driver.QueryerContext
			driver.SessionResetter
			driver.Validator
		}{c, contextQueryer{c}, reset, valid}
	case 15:
		return &struct {
			*conn
			driver.ExecerContext
			driver.QueryerContext
			driver.SessionResetter
			driver.Validator
		}{c, contextExecer{c}, contextQueryer{c}, reset, valid}
	default:
		return c
	}
}
