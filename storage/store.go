// Package storage implements portable SQL persistence for already-sanitized API events.
// Constructors never migrate automatically or take ownership of the supplied database.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	apilog "github.com/vishalanandl177/go-api-logger"
)

type Dialect string

const (
	Postgres      Dialect = "postgres"
	MySQL         Dialect = "mysql"
	SQLite        Dialect = "sqlite"
	CurrentSchema         = 1
)

var (
	ErrInvalid = errors.New("apilog storage: invalid argument")
	ErrSchema  = errors.New("apilog storage: schema is absent or incompatible; run explicit migrations")
)

// Error deliberately excludes database errors, SQL, connection strings and event data.
type Error struct{ Operation string }

func (e *Error) Error() string { return "apilog storage: " + e.Operation + " failed" }

type SQLStore struct {
	db      *sql.DB
	dialect Dialect
}

var _ apilog.Store = (*SQLStore)(nil)

func New(db *sql.DB, dialect Dialect) (*SQLStore, error) {
	if db == nil || (dialect != Postgres && dialect != MySQL && dialect != SQLite) {
		return nil, ErrInvalid
	}
	return &SQLStore{db: db, dialect: dialect}, nil
}
func NewPostgres(db *sql.DB) (*SQLStore, error) { return New(db, Postgres) }
func NewMySQL(db *sql.DB) (*SQLStore, error)    { return New(db, MySQL) }
func NewSQLite(db *sql.DB) (*SQLStore, error)   { return New(db, SQLite) }

func safeError(ctx context.Context, operation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &Error{Operation: operation}
}
func (s *SQLStore) bind(query string) string {
	if s.dialect != Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// WriteBatch is atomic. Duplicate IDs fail the complete batch. There are no implicit retries.
func (s *SQLStore) WriteBatch(ctx context.Context, events []apilog.Event) error {
	ctx = apilog.SuppressProfiling(ctx)
	if len(events) == 0 {
		return nil
	}
	type row struct {
		event           apilog.Event
		payload, search string
		count           any
	}
	rows := make([]row, len(events))
	for i, event := range events {
		if event.ID == "" || len(event.ID) > 128 || event.Time.IsZero() || event.Time.Year() < 1 || event.Time.Year() > 9999 || event.Duration < 0 || len(event.Method) > 32 || event.Version < 0 || event.Version > apilog.SchemaVersion {
			return ErrInvalid
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return ErrInvalid
		}
		search, err := json.Marshal([]any{event.URL, event.RequestHeaders, event.ResponseHeaders, event.Request.Data, event.Response.Data})
		if err != nil {
			return ErrInvalid
		}
		var count any
		if event.Profile != nil && event.Profile.Instrumented {
			if event.Profile.QueryCount < 0 {
				return ErrInvalid
			}
			count = event.Profile.QueryCount
		}
		rows[i] = row{event: event, payload: string(payload), search: strings.ToLower(string(search)), count: count}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return safeError(ctx, "write")
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, s.bind("INSERT INTO api_logger_events (id, timestamp_us, day_utc, duration_ns, method, status, sql_count, payload, search_text) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"))
	if err != nil {
		return safeError(ctx, "write")
	}
	defer stmt.Close()
	for _, row := range rows {
		e := row.event
		if _, err = stmt.ExecContext(ctx, e.ID, e.Time.UnixMicro(), e.Time.UTC().Format("2006-01-02"), int64(e.Duration), e.Method, e.Status, row.count, row.payload, row.search); err != nil {
			return safeError(ctx, "write")
		}
	}
	if err = tx.Commit(); err != nil {
		return safeError(ctx, "write")
	}
	return nil
}

func (s *SQLStore) Get(ctx context.Context, id string) (apilog.Event, error) {
	ctx = apilog.SuppressProfiling(ctx)
	var payload string
	err := s.db.QueryRowContext(ctx, s.bind("SELECT payload FROM api_logger_events WHERE id = ?"), id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return apilog.Event{}, apilog.ErrNotFound
	}
	if err != nil {
		return apilog.Event{}, safeError(ctx, "read")
	}
	var event apilog.Event
	if json.Unmarshal([]byte(payload), &event) != nil {
		return event, &Error{Operation: "decode"}
	}
	return event, nil
}

// List applies inclusive After and exclusive Before boundaries. Search is a literal
// case-insensitive substring over sanitized URL, headers, and request/response bodies.
func (s *SQLStore) List(ctx context.Context, filter apilog.Filter) (apilog.Page, error) {
	ctx = apilog.SuppressProfiling(ctx)
	where, args, err := buildWhere(filter)
	if err != nil {
		return apilog.Page{}, err
	}
	page, limit := filter.Page, filter.Limit
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = 50
	}
	if page < 1 || limit < 1 || limit > 500 || int64(page-1) > (1<<63-1)/int64(limit) {
		return apilog.Page{}, ErrInvalid
	}
	order := map[string]string{"time": "timestamp_us", "duration": "duration_ns", "status": "status", "method": "method", "sql": "sql_count"}[filter.Order]
	if filter.Order == "" {
		order = "timestamp_us"
	}
	if order == "" {
		return apilog.Page{}, ErrInvalid
	}
	direction := " ASC"
	if filter.Descending || filter.Order == "" {
		direction = " DESC"
	}
	orderClause := order + direction
	if filter.Order == "sql" {
		orderClause = "CASE WHEN sql_count IS NULL THEN 1 ELSE 0 END ASC, " + orderClause
	}
	result := apilog.Page{Events: []apilog.Event{}, Page: page, Limit: limit}
	if err = s.db.QueryRowContext(ctx, s.bind("SELECT COUNT(*) FROM api_logger_events"+where), args...).Scan(&result.Total); err != nil {
		return result, safeError(ctx, "list")
	}
	args = append(args, limit, int64(page-1)*int64(limit))
	rows, err := s.db.QueryContext(ctx, s.bind("SELECT payload FROM api_logger_events"+where+" ORDER BY "+orderClause+", id"+direction+" LIMIT ? OFFSET ?"), args...)
	if err != nil {
		return result, safeError(ctx, "list")
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		var event apilog.Event
		if rows.Scan(&payload) != nil || json.Unmarshal([]byte(payload), &event) != nil {
			return result, safeError(ctx, "decode")
		}
		result.Events = append(result.Events, event)
	}
	if rows.Err() != nil {
		return result, safeError(ctx, "list")
	}
	return result, nil
}

func buildWhere(f apilog.Filter) (string, []any, error) {
	if len(f.Search) > 4096 || len(f.Methods) > 100 || len(f.StatusCodes) > 600 || len(f.IDs) > 1000 || (!f.After.IsZero() && !f.Before.IsZero() && !f.After.Before(f.Before)) || (f.SQLMin != nil && *f.SQLMin < 0) || (f.SQLMax != nil && *f.SQLMax < 0) || (f.SQLMin != nil && f.SQLMax != nil && *f.SQLMin > *f.SQLMax) {
		return "", nil, ErrInvalid
	}
	var clauses []string
	var args []any
	add := func(clause string, value any) { clauses = append(clauses, clause); args = append(args, value) }
	if f.Search != "" {
		escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(f.Search))
		add("search_text LIKE ? ESCAPE '!'", "%"+escaped+"%")
	}
	if !f.After.IsZero() {
		add("timestamp_us >= ?", f.After.UnixMicro())
	}
	if !f.Before.IsZero() {
		add("timestamp_us < ?", f.Before.UnixMicro())
	}
	if f.Slow != nil {
		if f.SlowThreshold <= 0 {
			return "", nil, ErrInvalid
		}
		if *f.Slow {
			add("duration_ns >= ?", int64(f.SlowThreshold))
		} else {
			add("duration_ns < ?", int64(f.SlowThreshold))
		}
	}
	if f.Profiled != nil {
		if *f.Profiled {
			clauses = append(clauses, "sql_count IS NOT NULL")
		} else {
			clauses = append(clauses, "sql_count IS NULL")
		}
	}
	if f.SQLMin != nil {
		add("sql_count >= ?", *f.SQLMin)
	}
	if f.SQLMax != nil {
		add("sql_count <= ?", *f.SQLMax)
	}
	in := func(column string, values []any) {
		if len(values) == 0 {
			return
		}
		clauses = append(clauses, column+" IN ("+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+")")
		args = append(args, values...)
	}
	methods := make([]any, len(f.Methods))
	for i, v := range f.Methods {
		methods[i] = v
	}
	in("method", methods)
	statuses := make([]any, len(f.StatusCodes))
	for i, v := range f.StatusCodes {
		statuses[i] = v
	}
	in("status", statuses)
	ids := make([]any, len(f.IDs))
	for i, v := range f.IDs {
		ids[i] = v
	}
	in("id", ids)
	if len(clauses) == 0 {
		return "", args, nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}

func (s *SQLStore) Aggregate(ctx context.Context, filter apilog.Filter) (apilog.Analytics, error) {
	ctx = apilog.SuppressProfiling(ctx)
	where, args, err := buildWhere(filter)
	if err != nil {
		return apilog.Analytics{}, err
	}
	result := apilog.Analytics{Days: []apilog.DayStat{}, Statuses: []apilog.StatusStat{}}
	rows, err := s.db.QueryContext(ctx, s.bind("SELECT day_utc, COUNT(*), COALESCE(AVG(sql_count), 0), COUNT(sql_count) FROM api_logger_events"+where+" GROUP BY day_utc ORDER BY day_utc"), args...)
	if err != nil {
		return result, safeError(ctx, "aggregate")
	}
	for rows.Next() {
		var day apilog.DayStat
		if rows.Scan(&day.Day, &day.Count, &day.AverageSQL, &day.ProfiledCount) != nil {
			rows.Close()
			return result, safeError(ctx, "aggregate")
		}
		result.Days = append(result.Days, day)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, safeError(ctx, "aggregate")
	}
	rows, err = s.db.QueryContext(ctx, s.bind("SELECT status, COUNT(*) FROM api_logger_events"+where+" GROUP BY status ORDER BY status"), args...)
	if err != nil {
		return result, safeError(ctx, "aggregate")
	}
	defer rows.Close()
	for rows.Next() {
		var status apilog.StatusStat
		if rows.Scan(&status.Status, &status.Count) != nil {
			return result, safeError(ctx, "aggregate")
		}
		result.Statuses = append(result.Statuses, status)
	}
	if rows.Err() != nil {
		return result, safeError(ctx, "aggregate")
	}
	return result, nil
}

func (s *SQLStore) Delete(ctx context.Context, ids []string) (int64, error) {
	ctx = apilog.SuppressProfiling(ctx)
	if len(ids) == 0 {
		return 0, nil
	}
	if len(ids) > 1000 {
		return 0, ErrInvalid
	}
	where, args, err := buildWhere(apilog.Filter{IDs: ids})
	if err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, s.bind("DELETE FROM api_logger_events"+where), args...)
	if err != nil {
		return 0, safeError(ctx, "delete")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, safeError(ctx, "delete")
	}
	return count, nil
}

// Prune deletes at most BatchSize rows per statement until the fixed cutoff is
// exhausted. It is cancellable between batches. DryRun never mutates rows.
func (s *SQLStore) Prune(ctx context.Context, options apilog.PruneOptions) (apilog.PruneResult, error) {
	ctx = apilog.SuppressProfiling(ctx)
	var result apilog.PruneResult
	if options.Before.IsZero() || options.BatchSize < 0 || options.BatchSize > 1000 {
		return result, ErrInvalid
	}
	if options.BatchSize == 0 {
		options.BatchSize = 1000
	}
	if s.db.QueryRowContext(ctx, s.bind("SELECT COUNT(*) FROM api_logger_events WHERE timestamp_us < ?"), options.Before.UnixMicro()).Scan(&result.Matched) != nil {
		return result, safeError(ctx, "prune")
	}
	if options.DryRun {
		return result, nil
	}
	for {
		rows, err := s.db.QueryContext(ctx, s.bind("SELECT id FROM api_logger_events WHERE timestamp_us < ? ORDER BY timestamp_us, id LIMIT ?"), options.Before.UnixMicro(), options.BatchSize)
		if err != nil {
			return result, safeError(ctx, "prune")
		}
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil {
				rows.Close()
				return result, safeError(ctx, "prune")
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, safeError(ctx, "prune")
		}
		if len(ids) == 0 {
			return result, nil
		}
		count, err := s.Delete(ctx, ids)
		result.Deleted += count
		if err != nil {
			return result, err
		}
	}
}

// Check verifies connectivity, version and required columns without reading payloads.
func (s *SQLStore) Check(ctx context.Context) error {
	ctx = apilog.SuppressProfiling(ctx)
	if s.db.PingContext(ctx) != nil {
		return safeError(ctx, "connect")
	}
	var version int
	if s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM api_logger_schema").Scan(&version) != nil || version != CurrentSchema {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrSchema
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id, timestamp_us, day_utc, duration_ns, method, status, sql_count, payload, search_text FROM api_logger_events WHERE 1=0")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrSchema
	}
	rows.Close()
	return nil
}

// Version returns the latest applied schema version, never triggering migration.
func (s *SQLStore) Version(ctx context.Context) (int, error) {
	ctx = apilog.SuppressProfiling(ctx)
	var version int
	if s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM api_logger_schema").Scan(&version) != nil {
		return 0, ErrSchema
	}
	return version, nil
}
