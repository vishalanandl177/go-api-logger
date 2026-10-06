package storage

import (
	"context"
	"database/sql/driver"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
)

// Migrate serializes callers using a database advisory lock (PostgreSQL/MySQL)
// or a SQLite write transaction. Schema v1 is restartable after interrupted DDL.
// Applications should run migrations during deployment, before starting writers.
func (s *SQLStore) Migrate(ctx context.Context) error {
	ctx = apilog.SuppressProfiling(ctx)
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return safeError(ctx, "migrate")
	}
	defer conn.Close()
	unlock := func() {}
	committed := false
	switch s.dialect {
	case Postgres:
		if _, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock(714683271)"); err != nil {
			return safeError(ctx, "migration lock")
		}
		unlock = func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(cleanup, "SELECT pg_advisory_unlock(714683271)"); err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}
	case MySQL:
		var acquired int
		if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK('go_api_logger_schema', 10)").Scan(&acquired); err != nil || acquired != 1 {
			return safeError(ctx, "migration lock")
		}
		unlock = func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(cleanup, "SELECT RELEASE_LOCK('go_api_logger_schema')"); err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}
	case SQLite:
		if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return safeError(ctx, "migration lock")
		}
		unlock = func() {
			if !committed {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := conn.ExecContext(cleanup, "ROLLBACK"); err != nil {
					_ = conn.Raw(func(any) error { return driver.ErrBadConn })
				}
			}
		}
	}
	defer unlock()
	if _, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS api_logger_schema (version INTEGER PRIMARY KEY)"); err != nil {
		return safeError(ctx, "migrate")
	}
	var version int
	if err = conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM api_logger_schema").Scan(&version); err != nil {
		return safeError(ctx, "migrate")
	}
	if version > CurrentSchema {
		return ErrSchema
	}
	if version < 1 {
		textType := "TEXT"
		indexes := ""
		suffix := ""
		if s.dialect == MySQL {
			textType = "LONGTEXT"
			indexes = ", INDEX api_logger_time_idx (timestamp_us), INDEX api_logger_status_idx (status), INDEX api_logger_method_idx (method), INDEX api_logger_duration_idx (duration_ns), INDEX api_logger_sql_idx (sql_count)"
			suffix = " ENGINE=InnoDB DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"
		}
		ddl := "CREATE TABLE IF NOT EXISTS api_logger_events (id VARCHAR(128) PRIMARY KEY, timestamp_us BIGINT NOT NULL, day_utc VARCHAR(10) NOT NULL, duration_ns BIGINT NOT NULL, method VARCHAR(32) NOT NULL, status INTEGER NOT NULL, sql_count INTEGER NULL, payload " + textType + " NOT NULL, search_text " + textType + " NOT NULL" + indexes + ")" + suffix
		if _, err = conn.ExecContext(ctx, ddl); err != nil {
			return safeError(ctx, "migrate")
		}
		if s.dialect != MySQL {
			for _, index := range []struct{ name, column string }{{"time", "timestamp_us"}, {"status", "status"}, {"method", "method"}, {"duration", "duration_ns"}, {"sql", "sql_count"}} {
				if _, err = conn.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS api_logger_"+index.name+"_idx ON api_logger_events ("+index.column+")"); err != nil {
					return safeError(ctx, "migrate")
				}
			}
		}
		if _, err = conn.ExecContext(ctx, "INSERT INTO api_logger_schema (version) VALUES (1)"); err != nil {
			return safeError(ctx, "migrate")
		}
	}
	rows, err := conn.QueryContext(ctx, "SELECT id, timestamp_us, day_utc, duration_ns, method, status, sql_count, payload, search_text FROM api_logger_events WHERE 1=0")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrSchema
	}
	if err := rows.Close(); err != nil {
		return safeError(ctx, "migrate")
	}
	if s.dialect == SQLite {
		if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
			return safeError(ctx, "migrate")
		}
		committed = true
	}
	return nil
}
