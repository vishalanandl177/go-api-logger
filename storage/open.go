package storage

import (
	"context"
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// OpenPostgres opens and checks a pgx database/sql pool. The caller must close db.
func OpenPostgres(ctx context.Context, dsn string) (*SQLStore, *sql.DB, error) {
	return open(ctx, "pgx", dsn, Postgres)
}

// OpenMySQL accepts a go-sql-driver/mysql DSN. The caller must close db.
func OpenMySQL(ctx context.Context, dsn string) (*SQLStore, *sql.DB, error) {
	return open(ctx, "mysql", dsn, MySQL)
}

// OpenSQLite uses the pure Go modernc driver and a single connection, avoiding
// per-connection :memory: databases and concurrent writer lock contention.
// Existing caller-owned pools passed to NewSQLite are never reconfigured.
func OpenSQLite(ctx context.Context, dsn string) (*SQLStore, *sql.DB, error) {
	return open(ctx, "sqlite", dsn, SQLite)
}
func open(ctx context.Context, driver, dsn string, dialect Dialect) (*SQLStore, *sql.DB, error) {
	if dsn == "" {
		return nil, nil, ErrInvalid
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, nil, safeError(ctx, "open")
	}
	if dialect == SQLite {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, nil, safeError(ctx, "connect")
	}
	store, err := New(db, dialect)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return store, db, nil
}
