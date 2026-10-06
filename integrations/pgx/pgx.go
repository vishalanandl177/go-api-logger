// Package pgx integrates native pgx tracing without replacing existing tracers.
package pgx

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
	apilog "github.com/vishalanandl177/go-api-logger"
	"time"
)

type queryKey struct{}
type copyKey struct{}
type batchKey struct{}
type acquireKey struct{}
type queryState struct{ finish func(error) }
type batchState struct {
	start  time.Time
	finish func()
}

// Tracer profiles native calls. Query duration includes result consumption until
// rows close. Batch durations are overlapping lifetimes from SendBatch start.
type Tracer struct{}

// Install preserves query, batch, COPY, prepare, connect and pool tracers already
// configured by the application. Reinstalling is harmless.
func Install(config *pgx.ConnConfig) {
	if config == nil || contains(config.Tracer) {
		return
	}
	if config.Tracer == nil {
		config.Tracer = &Tracer{}
		return
	}
	config.Tracer = multitracer.New(config.Tracer, &Tracer{})
}
func contains(t pgx.QueryTracer) bool {
	if _, ok := t.(*Tracer); ok {
		return true
	}
	if multi, ok := t.(*multitracer.Tracer); ok {
		for _, child := range multi.QueryTracers {
			if contains(child) {
				return true
			}
		}
	}
	return false
}
func (*Tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryKey{}, queryState{apilog.StartQuery(ctx, "pgx", data.SQL)})
}
func (*Tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if s, ok := ctx.Value(queryKey{}).(queryState); ok {
		s.finish(data.Err)
	}
}
func (*Tracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	// COPY data and values are deliberately never inspected.
	return context.WithValue(ctx, copyKey{}, queryState{apilog.StartQuery(ctx, "pgx", "COPY "+data.TableName.Sanitize())})
}
func (*Tracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if s, ok := ctx.Value(copyKey{}).(queryState); ok {
		s.finish(data.Err)
	}
}
func (*Tracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	apilog.MarkInstrumented(ctx)
	return context.WithValue(ctx, batchKey{}, batchState{time.Now(), apilog.StartStage(ctx, "sql.batch")})
}
func (*Tracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	if s, ok := ctx.Value(batchKey{}).(batchState); ok {
		apilog.RecordQuery(ctx, "pgx.batch", data.SQL, s.start, data.Err)
	}
}
func (*Tracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchEndData) {
	if s, ok := ctx.Value(batchKey{}).(batchState); ok {
		s.finish()
	}
}
func (*Tracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	return context.WithValue(ctx, acquireKey{}, apilog.StartStage(ctx, "sql.pool.acquire"))
}
func (*Tracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireEndData) {
	if end, ok := ctx.Value(acquireKey{}).(func()); ok {
		end()
	}
}
