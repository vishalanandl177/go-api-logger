// Package gorm provides SQL profiling for GORM while preserving its logger.
package gorm

import (
	"context"
	apilog "github.com/vishalanandl177/go-api-logger"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"time"
)

type stateKey struct{}
type state struct {
	original context.Context
	start    time.Time
	dryRun   bool
	reported bool
}

// Logger wraps an existing logger. Prefer Plugin when the underlying SQL driver
// is also instrumented: the plugin suppresses nested driver profiling.
type Logger struct{ Next logger.Interface }

func WrapLogger(next logger.Interface) logger.Interface {
	if next == nil {
		next = logger.Discard
	}
	if _, ok := next.(*Logger); ok {
		return next
	}
	return &Logger{Next: next}
}
func (l *Logger) LogMode(level logger.LogLevel) logger.Interface {
	return &Logger{Next: l.Next.LogMode(level)}
}
func (l *Logger) Info(ctx context.Context, msg string, args ...interface{}) {
	l.Next.Info(ctx, msg, args...)
}
func (l *Logger) Warn(ctx context.Context, msg string, args ...interface{}) {
	l.Next.Warn(ctx, msg, args...)
}
func (l *Logger) Error(ctx context.Context, msg string, args ...interface{}) {
	l.Next.Error(ctx, msg, args...)
}

// ParamsFilter avoids asking GORM to interpolate bound values into SQL. Neither
// SQL text nor argument values are stored in API log events.
func (l *Logger) ParamsFilter(_ context.Context, sql string, _ ...interface{}) (string, []interface{}) {
	return sql, nil
}
func (l *Logger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	profileCtx := ctx
	dryRun := false
	reported := false
	if s, ok := ctx.Value(stateKey{}).(state); ok {
		profileCtx, begin, dryRun = s.original, s.start, s.dryRun
		reported = s.reported
	}
	if !dryRun && !reported && !apilog.ProfilingSuppressed(profileCtx) && apilog.ProfileEnabled(profileCtx) {
		sql, _ := fc()
		apilog.RecordQuery(profileCtx, "gorm", sql, begin, err)
	}
	l.Next.Trace(ctx, begin, fc, err)
}

// Plugin installs the context logger and suppresses nested database/sql or pgx
// profiling for GORM operations. Use db.WithContext(request.Context()).
type Plugin struct{}

func (Plugin) Name() string { return "go-api-logger" }
func (Plugin) Initialize(db *gorm.DB) error {
	db.Logger = WrapLogger(db.Logger)
	before := func(tx *gorm.DB) {
		if tx.Statement == nil {
			return
		}
		original := tx.Statement.Context
		if parent, ok := original.Value(stateKey{}).(state); ok {
			original = parent.original
		}
		s := state{original: original, start: time.Now(), dryRun: tx.DryRun}
		tx.Statement.Context = context.WithValue(apilog.SuppressProfiling(original), stateKey{}, s)
	}
	after := func(tx *gorm.DB) {
		if tx.Statement == nil {
			return
		}
		if s, ok := tx.Statement.Context.Value(stateKey{}).(state); ok {
			// Count independently of Logger.Trace: an application may replace its
			// logger for one Session without disabling the installed plugin.
			if !s.dryRun && tx.Statement.SQL.Len() > 0 {
				apilog.RecordQuery(s.original, "gorm", tx.Statement.SQL.String(), s.start, tx.Error)
			}
			s.reported = true
			// Restore the unsuppressed context for subsequent application queries;
			// retain operation timing until GORM invokes Logger.Trace.
			tx.Statement.Context = context.WithValue(s.original, stateKey{}, s)
		}
	}
	// GORM's processor types are private, so register each supported processor
	// explicitly. This avoids reflection into framework internals.
	register := []func() error{
		func() error { return db.Callback().Create().Before("*").Register("apilog:before", before) },
		func() error { return db.Callback().Create().After("*").Register("apilog:after", after) },
		func() error { return db.Callback().Query().Before("*").Register("apilog:before", before) },
		func() error { return db.Callback().Query().After("*").Register("apilog:after", after) },
		func() error { return db.Callback().Update().Before("*").Register("apilog:before", before) },
		func() error { return db.Callback().Update().After("*").Register("apilog:after", after) },
		func() error { return db.Callback().Delete().Before("*").Register("apilog:before", before) },
		func() error { return db.Callback().Delete().After("*").Register("apilog:after", after) },
		func() error { return db.Callback().Raw().Before("*").Register("apilog:before", before) },
		func() error { return db.Callback().Raw().After("*").Register("apilog:after", after) },
		func() error { return db.Callback().Row().Before("*").Register("apilog:before", before) },
		func() error { return db.Callback().Row().After("*").Register("apilog:after", after) },
	}
	for _, add := range register {
		if err := add(); err != nil {
			return err
		}
	}
	return nil
}
