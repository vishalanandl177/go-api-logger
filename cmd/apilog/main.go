// Command apilog runs explicit schema migrations, retention and database diagnostics.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/storage"
)

type diagnostic struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
type report struct {
	Command       string       `json:"command"`
	Results       []diagnostic `json:"results"`
	Matched       int64        `json:"matched,omitempty"`
	Deleted       int64        `json:"deleted,omitempty"`
	DryRun        bool         `json:"dry_run,omitempty"`
	SchemaVersion int          `json:"schema_version,omitempty"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, time.Now)) }

func run(args []string, out, errOut io.Writer, getenv func(string) string, now func() time.Time) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, "Usage: apilog <migrate|prune|doctor> --driver <sqlite|postgres|mysql> [options]\nConnection strings are read from API_LOGGER_DSN or --dsn-env NAME.\nPrune requires exactly one of --days N or --before YYYY-MM-DD/RFC3339.\nUse --dry-run to preview retention. All commands support --format text|json.")
		return 0
	}
	command := args[0]
	if command != "migrate" && command != "prune" && command != "doctor" {
		fmt.Fprintln(errOut, "apilog: unknown command")
		return 2
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	driver := flags.String("driver", "sqlite", "database driver")
	dsnEnv := flags.String("dsn-env", "API_LOGGER_DSN", "environment variable containing connection string")
	format := flags.String("format", "text", "text or json")
	timeout := flags.Duration("timeout", 30*time.Second, "operation timeout")
	days := flags.Int("days", 0, "delete rows older than N days")
	before := flags.String("before", "", "exclusive UTC date or RFC3339 cutoff")
	batchSize := flags.Int("batch-size", 1000, "retention batch size, 1 to 1000")
	dryRun := flags.Bool("dry-run", false, "preview without deletion")
	failLevel := flags.String("fail-level", "error", "doctor failure threshold: warning or error")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Options: --driver --dsn-env --format --timeout --days --before --batch-size --dry-run --fail-level")
			return 0
		}
		fmt.Fprintln(errOut, "apilog: invalid command options; use --help")
		return 2
	}
	seen := map[string]bool{}
	invalidCommandFlag := false
	flags.Visit(func(f *flag.Flag) {
		seen[f.Name] = true
		switch f.Name {
		case "days", "before", "batch-size", "dry-run":
			invalidCommandFlag = invalidCommandFlag || command != "prune"
		case "fail-level":
			invalidCommandFlag = invalidCommandFlag || command != "doctor"
		}
	})
	if invalidCommandFlag || flags.NArg() != 0 || (*format != "text" && *format != "json") || (*driver != "sqlite" && *driver != "postgres" && *driver != "mysql") || *timeout <= 0 || (*failLevel != "warning" && *failLevel != "error") {
		fmt.Fprintln(errOut, "apilog: invalid command options")
		return 2
	}
	var cutoff time.Time
	if command == "prune" {
		if seen["days"] == seen["before"] || (seen["days"] && *days <= 0) || (seen["before"] && *before == "") || *days > 365000 || *batchSize < 1 || *batchSize > 1000 {
			fmt.Fprintln(errOut, "apilog: prune requires one positive --days or valid --before, and --batch-size 1 to 1000")
			return 2
		}
		if *days > 0 {
			cutoff = now().UTC().AddDate(0, 0, -*days)
		} else {
			var err error
			cutoff, err = time.Parse("2006-01-02", *before)
			if err != nil {
				cutoff, err = time.Parse(time.RFC3339, *before)
			}
			if err != nil {
				fmt.Fprintln(errOut, "apilog: --before must be YYYY-MM-DD or RFC3339")
				return 2
			}
		}
	}
	r := report{Command: command, Results: []diagnostic{}}
	add := func(code, level, message string) { r.Results = append(r.Results, diagnostic{code, level, message}) }
	dsn := getenv(*dsnEnv)
	if dsn == "" {
		add("DB_CONFIG", "error", "The configured DSN environment variable is empty.")
		writeReport(out, *format, r)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var store *storage.SQLStore
	var db *sql.DB
	var err error
	switch *driver {
	case "postgres":
		store, db, err = storage.OpenPostgres(ctx, dsn)
	case "mysql":
		store, db, err = storage.OpenMySQL(ctx, dsn)
	case "sqlite":
		if command == "doctor" || (command == "prune" && *dryRun) {
			store, db, err = storage.OpenSQLiteReadOnly(ctx, dsn)
		} else {
			store, db, err = storage.OpenSQLite(ctx, dsn)
		}
	}
	if err != nil {
		add("DB_CONNECT", "error", "Database connection failed. Verify credentials, network, driver and timeout.")
		writeReport(out, *format, r)
		return 1
	}
	defer db.Close()
	if command == "migrate" {
		if err = store.Migrate(ctx); err != nil {
			add("DB_MIGRATE", "error", "Schema migration failed. Check database permissions, schema version and migration lock.")
		} else {
			r.SchemaVersion = storage.CurrentSchema
			add("DB_MIGRATE", "ok", "Schema migration completed.")
		}
	} else if command == "prune" {
		if err = store.Check(ctx); err != nil {
			add("DB_SCHEMA", "error", "Schema is not ready. Run explicit migrations first.")
		} else {
			var result apilog.PruneResult
			result, err = store.Prune(ctx, apilog.PruneOptions{Before: cutoff, BatchSize: *batchSize, DryRun: *dryRun})
			r.Matched = result.Matched
			r.Deleted = result.Deleted
			r.DryRun = *dryRun
			if err != nil {
				add("DB_PRUNE", "error", "Retention stopped before completion; reported deleted rows have already been removed.")
			} else {
				add("DB_PRUNE", "ok", "Retention operation completed.")
			}
		}
	} else {
		add("DB_CONNECT", "ok", "Database connectivity is healthy.")
		if err = store.Check(ctx); err != nil {
			add("DB_SCHEMA", "error", "Schema is absent, incompatible or unreadable. Run explicit migrations with deployment credentials.")
		} else {
			r.SchemaVersion = storage.CurrentSchema
			add("DB_SCHEMA", "ok", "Schema version and required columns are ready.")
		}
		add("RUNTIME_NOT_INSPECTED", "warning", "Standalone doctor cannot inspect a running application's worker, queue or capture configuration. Inspect its in-process Health and validated configuration.")
	}
	if writeReport(out, *format, r) != nil {
		fmt.Fprintln(errOut, "apilog: output failed")
		return 1
	}
	for _, item := range r.Results {
		if item.Level == "error" || (command == "doctor" && *failLevel == "warning" && item.Level == "warning") {
			return 1
		}
	}
	return 0
}

func writeReport(out io.Writer, format string, r report) error {
	if format == "json" {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(r)
	}
	for _, result := range r.Results {
		if _, err := fmt.Fprintf(out, "%s %s %s\n", result.Level, result.Code, result.Message); err != nil {
			return err
		}
	}
	if r.Command == "prune" {
		_, err := fmt.Fprintf(out, "matched=%d deleted=%d dry_run=%t\n", r.Matched, r.Deleted, r.DryRun)
		return err
	}
	return nil
}
