package pgx

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/vishalanandl177/go-api-logger/integrations/internal/testutil"
	"os"
	"testing"
)

type prior struct{ queries, batches, copies int }

func (p *prior) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	p.queries++
	return ctx
}
func (*prior) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (p *prior) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	p.batches++
	return ctx
}
func (*prior) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}
func (*prior) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData)     {}
func (p *prior) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	p.copies++
	return ctx
}
func (*prior) TraceCopyFromEnd(context.Context, *pgx.Conn, pgx.TraceCopyFromEndData) {}
func TestCompositionAndRepeatedInstall(t *testing.T) {
	ctx, finish := testutil.Exchange(t)
	old := &prior{}
	cfg := &pgx.ConnConfig{Tracer: old}
	Install(cfg)
	Install(cfg)
	qctx := cfg.Tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "SELECT $1", Args: []any{"secret"}})
	cfg.Tracer.TraceQueryEnd(qctx, nil, pgx.TraceQueryEndData{})
	b := cfg.Tracer.(pgx.BatchTracer)
	bctx := b.TraceBatchStart(ctx, nil, pgx.TraceBatchStartData{})
	b.TraceBatchQuery(bctx, nil, pgx.TraceBatchQueryData{SQL: "SELECT 1"})
	b.TraceBatchQuery(bctx, nil, pgx.TraceBatchQueryData{SQL: "SELECT 2"})
	b.TraceBatchEnd(bctx, nil, pgx.TraceBatchEndData{})
	c := cfg.Tracer.(pgx.CopyFromTracer)
	cctx := c.TraceCopyFromStart(ctx, nil, pgx.TraceCopyFromStartData{TableName: pgx.Identifier{"users"}})
	c.TraceCopyFromEnd(cctx, nil, pgx.TraceCopyFromEndData{})
	e := finish()
	if e.Profile.QueryCount != 4 {
		t.Fatalf("profile=%+v", e.Profile)
	}
	if old.queries != 1 || old.batches != 1 || old.copies != 1 {
		t.Fatalf("prior=%+v", old)
	}
}
func TestPostgresNativeQueriesBatchAndCopy(t *testing.T) {
	dsn := os.Getenv("APILOG_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set APILOG_TEST_POSTGRES_DSN for a live PostgreSQL conformance test")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test DSN")
	}
	Install(cfg)
	c, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal("PostgreSQL test connection failed")
	}
	defer c.Close(context.Background())
	ctx, finish := testutil.Exchange(t)
	if _, err := c.Exec(ctx, "CREATE TEMP TABLE apilog_profile_test (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CopyFrom(ctx, pgx.Identifier{"apilog_profile_test"}, []string{"id"}, pgx.CopyFromRows([][]any{{1}, {2}})); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := c.QueryRow(ctx, "SELECT COUNT(*) FROM apilog_profile_test").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatal(n)
	}
	b := &pgx.Batch{}
	b.Queue("SELECT 1")
	b.Queue("SELECT 2")
	br := c.SendBatch(ctx, b)
	for range 2 {
		if err := br.QueryRow().Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
	if err := br.Close(); err != nil {
		t.Fatal(err)
	}
	e := finish()
	if e.Profile.QueryCount != 5 {
		t.Fatalf("profile=%+v", e.Profile)
	}
}
