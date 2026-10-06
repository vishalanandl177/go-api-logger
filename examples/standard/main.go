// Command standard runs a local API, SQL profiling, protected dashboard and metrics.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/dashboard"
	"github.com/vishalanandl177/go-api-logger/httpmw"
	apiprom "github.com/vishalanandl177/go-api-logger/integrations/prometheus"
	apisl "github.com/vishalanandl177/go-api-logger/integrations/sql"
	"github.com/vishalanandl177/go-api-logger/storage"
	"modernc.org/sqlite"
)

type options struct {
	Directory, Password string
	Migrate             bool
}
type demo struct {
	Handler      http.Handler
	Logger       *apilog.Logger
	Store        *storage.SQLStore
	appDB, logDB *sql.DB
}
type adminContext struct{}
type item struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func newDemo(ctx context.Context, opts options) (_ *demo, err error) {
	if len(opts.Password) < 16 {
		return nil, errors.New("set API_LOGGER_ADMIN_PASSWORD to at least 16 characters")
	}
	if opts.Directory == "" {
		opts.Directory = "."
	}
	if opts.Migrate {
		if os.MkdirAll(opts.Directory, 0700) != nil {
			return nil, errors.New("cannot create the data directory")
		}
	}
	d := &demo{}
	defer func() {
		if err != nil {
			if d.Logger != nil {
				closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = d.Logger.Shutdown(closeCtx)
			}
			if d.appDB != nil {
				d.appDB.Close()
			}
			if d.logDB != nil {
				d.logDB.Close()
			}
		}
	}()
	d.Store, d.logDB, err = storage.OpenSQLite(ctx, filepath.Join(opts.Directory, "api-logs.db"))
	if err != nil {
		return nil, err
	}
	if opts.Migrate {
		if err = d.Store.Migrate(ctx); err != nil {
			return nil, err
		}
	}
	if err = d.Store.Check(ctx); err != nil {
		return nil, errors.New("log schema is not ready; run this example with -migrate")
	}
	connector, err := sqlite.NewConnector(filepath.Join(opts.Directory, "application.db"))
	if err != nil {
		return nil, errors.New("application database configuration failed")
	}
	d.appDB = apisl.OpenDB(connector)
	d.appDB.SetMaxOpenConns(1)
	d.appDB.SetMaxIdleConns(1)
	if opts.Migrate {
		if _, err = d.appDB.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS demo_items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)"); err != nil {
			return nil, errors.New("application schema creation failed")
		}
	}
	rows, err := d.appDB.QueryContext(ctx, "SELECT id, name FROM demo_items WHERE 1=0")
	if err != nil {
		return nil, errors.New("application schema is not ready; run this example with -migrate")
	}
	rows.Close()
	registry := prometheus.NewRegistry()
	observer, err := apiprom.New(registry, apiprom.Options{Routes: []string{"GET /items", "POST /items", "GET /items/{id}", "GET /slow", "GET /error"}, Outputs: []string{"database"}})
	if err != nil {
		return nil, err
	}
	cfg := apilog.DefaultConfig()
	cfg.Outputs = []apilog.Output{{Name: "database", Kind: "storage", Sink: d.Store}}
	cfg.Queue.FlushInterval = time.Second
	cfg.Profile.Enabled = true
	cfg.Correlation.Enabled = true
	cfg.SkipPaths = []string{"/api-logs", "/internal", "/healthz"}
	cfg.Observer = observer
	d.Logger, err = apilog.New(cfg)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", d.listItems)
	mux.HandleFunc("POST /items", d.createItem)
	mux.HandleFunc("GET /items/{id}", d.getItem)
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		d.listItems(w, r)
	})
	mux.HandleFunc("GET /error", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 500, map[string]string{"error": "demonstration failure"})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	board, err := dashboard.New(d.Store, dashboard.Options{Authorize: func(r *http.Request, action dashboard.Action) bool {
		ok, _ := r.Context().Value(adminContext{}).(bool)
		return ok && action != dashboard.Delete
	}})
	if err != nil {
		return nil, err
	}
	auth := basicAuth(opts.Password)
	mux.Handle("/api-logs/", auth(board))
	mux.Handle("/internal/metrics", auth(promhttp.HandlerFor(registry, promhttp.HandlerOpts{})))
	mux.Handle("/internal/health", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.Store.Check(r.Context()) != nil {
			respond(w, 503, map[string]string{"error": "log store unavailable"})
			return
		}
		respond(w, 200, map[string]any{"health": d.Logger.Health(), "diagnostics": d.Logger.Diagnose()})
	})))
	d.Handler = httpmw.Middleware(d.Logger)(mux)
	return d, nil
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (d *demo) createItem(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name  string `json:"name"`
		Token string `json:"token,omitempty"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || strings.TrimSpace(input.Name) == "" || len(input.Name) > 256 {
		respond(w, 400, map[string]string{"error": "provide one JSON object with name (1 to 256 bytes) and optional token"})
		return
	}
	result, err := d.appDB.ExecContext(r.Context(), "INSERT INTO demo_items (name) VALUES (?)", input.Name)
	if err != nil {
		respond(w, 500, map[string]string{"error": "cannot create item"})
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		respond(w, 500, map[string]string{"error": "cannot read created item"})
		return
	}
	respond(w, 201, item{ID: id, Name: input.Name})
}
func (d *demo) listItems(w http.ResponseWriter, r *http.Request) {
	rows, err := d.appDB.QueryContext(r.Context(), "SELECT id, name FROM demo_items ORDER BY id DESC LIMIT 100")
	if err != nil {
		respond(w, 500, map[string]string{"error": "cannot list items"})
		return
	}
	defer rows.Close()
	items := []item{}
	for rows.Next() {
		var value item
		if rows.Scan(&value.ID, &value.Name) != nil {
			respond(w, 500, map[string]string{"error": "cannot read items"})
			return
		}
		items = append(items, value)
	}
	if rows.Err() != nil {
		respond(w, 500, map[string]string{"error": "cannot finish items"})
		return
	}
	respond(w, 200, items)
}
func (d *demo) getItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		respond(w, 404, map[string]string{"error": "item not found"})
		return
	}
	var value item
	err = d.appDB.QueryRowContext(r.Context(), "SELECT id, name FROM demo_items WHERE id = ?", id).Scan(&value.ID, &value.Name)
	if errors.Is(err, sql.ErrNoRows) {
		respond(w, 404, map[string]string{"error": "item not found"})
		return
	}
	if err != nil {
		respond(w, 500, map[string]string{"error": "cannot read item"})
		return
	}
	respond(w, 200, value)
}
func basicAuth(password string) func(http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(password))
	expectedUser := sha256.Sum256([]byte("admin"))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			userHash := sha256.Sum256([]byte(user))
			passHash := sha256.Sum256([]byte(pass))
			if !ok || subtle.ConstantTimeCompare(userHash[:], expectedUser[:])&subtle.ConstantTimeCompare(passHash[:], expected[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="API Logger", charset="UTF-8"`)
				respond(w, 401, map[string]string{"error": "authentication required"})
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminContext{}, true)))
		})
	}
}

// Close must be called only after the HTTP server has drained its requests.
func (d *demo) Close(ctx context.Context) error {
	if err := d.Logger.Shutdown(ctx); err != nil {
		return err
	}
	appErr := d.appDB.Close()
	logErr := d.logDB.Close()
	if appErr != nil || logErr != nil {
		return errors.New("database close failed")
	}
	return nil
}

func serve(ctx context.Context, d *demo, addr string) error {
	server := &http.Server{Addr: addr, Handler: d.Handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	failed := make(chan error, 1)
	go func() { failed <- server.ListenAndServe() }()
	select {
	case err := <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP listener failed")
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return errors.New("HTTP shutdown exceeded its deadline")
	}
	return nil
}
func runMain() error {
	directory := flag.String("data-dir", "./demo-data", "local SQLite directory")
	migrate := flag.Bool("migrate", false, "explicitly create/update demo schemas")
	port := flag.Int("port", 8080, "loopback HTTP port")
	flag.Parse()
	if *port < 1 || *port > 65535 {
		return errors.New("invalid port")
	}
	startup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	d, err := newDemo(startup, options{Directory: *directory, Password: os.Getenv("API_LOGGER_ADMIN_PASSWORD"), Migrate: *migrate})
	cancel()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	log.Printf("API listening at http://%s; dashboard /api-logs/; credentials come from the environment", addr)
	serveErr := serve(ctx, d, addr)
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err = d.Close(shutdown); err != nil {
		return errors.New("logger shutdown incomplete")
	}
	return serveErr
}
func main() {
	if err := runMain(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
