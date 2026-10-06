package storage

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"

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

// OpenSQLiteReadOnly opens an existing file using SQLite's mode=ro URI option.
// It accepts plain filenames or file: URIs, with only mode and cache options.
// Any supplied ro/rw/rwc mode is replaced with ro. In-memory databases, duplicate
// options, driver PRAGMA options and all other options are rejected before open.
// The caller owns db. Reads and dry-run pruning are supported; writes are rejected
// by the database connection. Use a writable pool for migrations and deletion.
func OpenSQLiteReadOnly(ctx context.Context, dsn string) (*SQLStore, *sql.DB, error) {
	readonlyDSN, err := sqliteReadOnlyDSN(dsn)
	if err != nil {
		return nil, nil, err
	}
	return open(ctx, "sqlite", readonlyDSN, SQLite)
}

func sqliteReadOnlyDSN(dsn string) (string, error) {
	if dsn == "" || strings.ContainsRune(dsn, 0) {
		return "", ErrInvalid
	}
	filename, query, _ := strings.Cut(dsn, "?")
	options, err := url.ParseQuery(query)
	if err != nil {
		return "", ErrInvalid
	}
	for key, values := range options {
		if len(values) != 1 {
			return "", ErrInvalid
		}
		switch key {
		case "mode":
			if values[0] != "ro" && values[0] != "rw" && values[0] != "rwc" {
				return "", ErrInvalid
			}
		case "cache":
			if values[0] != "private" && values[0] != "shared" {
				return "", ErrInvalid
			}
		default:
			// Driver options can execute PRAGMAs at open time. Do not carry them
			// into a diagnostic connection, even if SQLite would reject a write.
			return "", ErrInvalid
		}
	}
	var uri *url.URL
	if strings.HasPrefix(filename, "file:") {
		uri, err = url.Parse(filename)
		if err != nil || uri.User != nil || (uri.Host != "" && uri.Host != "localhost") || uri.Fragment != "" {
			return "", ErrInvalid
		}
		name := uri.Path
		if uri.Opaque != "" {
			name, err = url.PathUnescape(uri.Opaque)
		}
		if err != nil || name == "" || name == ":memory:" || strings.ContainsRune(name, 0) {
			return "", ErrInvalid
		}
	} else {
		if filename == "" || filename == ":memory:" {
			return "", ErrInvalid
		}
		absolute, err := filepath.Abs(filename)
		if err != nil {
			return "", ErrInvalid
		}
		path := filepath.ToSlash(absolute)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path // Drive-letter filenames use file:///C:/... on Windows.
		}
		uri = &url.URL{Scheme: "file", Path: path}
	}
	options.Set("mode", "ro")
	uri.RawQuery = options.Encode()
	return uri.String(), nil
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
