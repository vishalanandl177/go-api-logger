package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/integrations/internal/testutil"
	profilesql "github.com/vishalanandl177/go-api-logger/integrations/sql"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"modernc.org/sqlite"
	"testing"
	"time"
)

type spyLogger struct {
	logger.Interface
	traces int
}

func (l *spyLogger) Trace(context.Context, time.Time, func() (string, int64), error) { l.traces++ }
func TestPluginSuppressesNestedDriverAndPreservesLogger(t *testing.T) {
	d := profilesql.WrapDriver(&sqlite.Driver{}).(driver.DriverContext)
	c, err := d.OpenConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	pool := sql.OpenDB(c)
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	spy := &spyLogger{Interface: logger.Discard}
	db, err := gorm.Open(gormsqlite.New(gormsqlite.Config{Conn: pool, DriverName: "sqlite"}), &gorm.Config{Logger: spy, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Use(Plugin{}); err != nil {
		t.Fatal(err)
	}
	ctx, finish := testutil.Exchange(t)
	session := db.WithContext(ctx)
	if err := session.Exec("CREATE TABLE items (id INTEGER, name TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := session.Exec("INSERT INTO items VALUES (?, ?)", 1, "secret-value").Error; err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID   int
		Name string
	}
	if err := session.Table("items").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "secret-value" {
		t.Fatalf("rows=%+v", rows)
	}
	if err := session.Session(&gorm.Session{DryRun: true}).Exec("DELETE FROM items").Error; err != nil {
		t.Fatal(err)
	}
	if err := session.Session(&gorm.Session{Logger: logger.Discard}).Exec("INSERT INTO items VALUES (?, ?)", 2, "another").Error; err != nil {
		t.Fatal(err)
	}
	e := finish()
	if e.Profile.QueryCount != 4 {
		t.Fatalf("nested driver/dry run counted: %+v", e.Profile)
	}
	if spy.traces != 4 {
		t.Fatalf("existing logger calls=%d", spy.traces)
	}
}

func TestLoggerRespectsExplicitSuppression(t *testing.T) {
	ctx, finish := testutil.Exchange(t)
	l := WrapLogger(logger.Discard)
	l.Trace(apilog.SuppressProfiling(ctx), time.Now(), func() (string, int64) { return "SELECT secret", 1 }, nil)
	e := finish()
	if e.Profile.QueryCount != 0 {
		t.Fatalf("profile=%+v", e.Profile)
	}
}
