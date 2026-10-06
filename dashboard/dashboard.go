// Package dashboard provides an embedded, application-authorized API log browser.
// It never starts a server or changes the store schema.
package dashboard

import (
	"bytes"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	apilog "github.com/vishalanandl177/go-api-logger"
)

// Action identifies a dashboard permission. Export and Delete also require View.
type Action string

const (
	View   Action = "view"
	Export Action = "export"
	Delete Action = "delete"
)

// Options configures a dashboard mounted inside the application's HTTP server.
type Options struct {
	// Authorize must read the application's authenticated request context. A nil
	// callback is rejected; a false result denies access with HTTP 403.
	Authorize func(*http.Request, Action) bool
	// BasePath is the mount prefix, without a trailing slash. Default: /api-logs.
	BasePath string
	// Timezone affects displayed timestamps and date filter input. Charts use UTC.
	Timezone *time.Location
	// SlowThreshold defaults to 200 milliseconds and matches the slow/fast filter.
	SlowThreshold time.Duration
}

//go:embed templates/*.html assets/*
var resources embed.FS

type handler struct {
	store     apilog.Store
	opts      Options
	templates *template.Template
	assets    http.Handler
	csrf      *http.CrossOriginProtection
}

// New returns a dashboard handler. Mount it at Options.BasePath + "/" after
// your application's authentication middleware. Construction performs no I/O.
func New(store apilog.Store, options Options) (http.Handler, error) {
	if store == nil {
		return nil, errors.New("dashboard: store is required")
	}
	if options.Authorize == nil {
		return nil, errors.New("dashboard: authorization is required")
	}
	if options.BasePath == "" {
		options.BasePath = "/api-logs"
	}
	options.BasePath = strings.TrimSuffix(options.BasePath, "/")
	if options.BasePath != "" && (!strings.HasPrefix(options.BasePath, "/") || path.Clean(options.BasePath) != options.BasePath || strings.ContainsAny(options.BasePath, "?#\\%") || strings.ContainsFunc(options.BasePath, unicode.IsControl)) {
		return nil, errors.New("dashboard: BasePath must be a clean absolute URL path")
	}
	if options.Timezone == nil {
		options.Timezone = time.UTC
	}
	if options.SlowThreshold == 0 {
		options.SlowThreshold = 200 * time.Millisecond
	}
	if options.SlowThreshold < 0 {
		return nil, errors.New("dashboard: SlowThreshold must be positive")
	}
	h := &handler{store: store, opts: options, csrf: http.NewCrossOriginProtection()}
	functions := template.FuncMap{
		"methods": func() []string {
			return []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT"}
		},
		"json":     prettyJSON,
		"date":     func(t time.Time) string { return t.In(options.Timezone).Format("02 Jan 2006, 15:04:05.000") },
		"duration": func(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d)/float64(time.Millisecond)) },
		"slow":     func(d time.Duration) bool { return d >= options.SlowThreshold },
		"statusClass": func(s int) string {
			if s == 0 {
				return "status-warning"
			}
			if s >= 500 {
				return "status-error"
			}
			if s >= 400 {
				return "status-warning"
			}
			return "status-ok"
		},
		"statusLabel": func(s int) string {
			if s == 0 {
				return "Not sent"
			}
			return strconv.Itoa(s)
		},
		"sortArrow": func(key, current, direction string) string {
			if key != current {
				return "↕"
			}
			if direction == "ascending" {
				return "↑"
			}
			return "↓"
		},
		"eventURL": func(id string) string { return options.BasePath + "/events/" + url.PathEscape(id) },
		"queryCount": func(p *apilog.Profile) string {
			if p == nil || !p.Instrumented {
				return "Not tracked"
			}
			return strconv.Itoa(p.QueryCount)
		},
	}
	var err error
	h.templates, err = template.New("dashboard").Funcs(functions).ParseFS(resources, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("dashboard: templates: %w", err)
	}
	assets, err := fs.Sub(resources, "assets")
	if err != nil {
		return nil, err
	}
	h.assets = http.StripPrefix(options.BasePath+"/assets/", http.FileServer(http.FS(assets)))
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	apilog.SkipRequest(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if !h.opts.Authorize(r, View) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	if r.URL.Path == h.opts.BasePath && r.Method == http.MethodGet {
		target := h.opts.BasePath + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	relative, ok := strings.CutPrefix(r.URL.Path, h.opts.BasePath+"/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(relative, "assets/") {
		if !allowMethod(w, r, http.MethodGet) {
			return
		}
		if relative != "assets/dashboard.css" && relative != "assets/dashboard.js" {
			http.NotFound(w, r)
			return
		}
		h.assets.ServeHTTP(w, r)
		return
	}
	switch {
	case relative == "":
		if allowMethod(w, r, http.MethodGet) {
			h.list(w, r)
		}
	case strings.HasPrefix(relative, "events/"):
		if allowMethod(w, r, http.MethodGet) {
			h.detail(w, r, strings.TrimPrefix(relative, "events/"))
		}
	case strings.HasPrefix(relative, "charts/"):
		if allowMethod(w, r, http.MethodGet) {
			h.chart(w, r, strings.TrimPrefix(relative, "charts/"))
		}
	case relative == "export":
		if h.allowAction(w, r, Export) {
			h.export(w, r)
		}
	case relative == "delete":
		if h.allowAction(w, r, Delete) {
			h.delete(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (h *handler) allowAction(w http.ResponseWriter, r *http.Request, action Action) bool {
	if !allowMethod(w, r, http.MethodPost) {
		return false
	}
	if !h.opts.Authorize(r, action) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return false
	}
	if h.csrf.Check(r) != nil {
		http.Error(w, "Cross-origin form submission denied", http.StatusForbidden)
		return false
	}
	return true
}

type view struct {
	Base, Timezone, Title      string
	Page                       apilog.Page
	Event                      apilog.Event
	Query                      url.Values
	CanExport, CanDelete       bool
	Previous, Next, ChartQuery string
	SortLinks                  map[string]string
	SlowThreshold              string
	CurrentSort, SortDirection string
}

func (h *handler) baseView(r *http.Request) view {
	return view{Base: h.opts.BasePath, Timezone: h.opts.Timezone.String(), Query: r.URL.Query(), CanExport: h.opts.Authorize(r, Export), CanDelete: h.opts.Authorize(r, Delete), SlowThreshold: fmt.Sprintf("%.0f ms", float64(h.opts.SlowThreshold)/float64(time.Millisecond))}
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	filter, err := h.parseFilter(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, err := h.store.List(r.Context(), filter)
	if err != nil {
		serverError(w)
		return
	}
	v := h.baseView(r)
	v.Title = "Requests"
	v.Page = page
	v.Page.Page, v.Page.Limit = filter.Page, filter.Limit
	v.SortLinks = make(map[string]string)
	v.CurrentSort, v.SortDirection = filter.Order, "descending"
	if !filter.Descending {
		v.SortDirection = "ascending"
	}
	for _, key := range []string{"time", "method", "status", "duration", "sql"} {
		q := r.URL.Query()
		q.Set("sort", key)
		q.Set("direction", "desc")
		q.Del("page")
		if key == filter.Order && filter.Descending {
			q.Set("direction", "asc")
		}
		v.SortLinks[key] = h.opts.BasePath + "/?" + q.Encode()
	}
	if filter.Page > 1 {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(filter.Page-1))
		v.Previous = h.opts.BasePath + "/?" + q.Encode()
	}
	if int64(filter.Page)*int64(filter.Limit) < page.Total {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(filter.Page+1))
		v.Next = h.opts.BasePath + "/?" + q.Encode()
	}
	q := r.URL.Query()
	q.Del("page")
	q.Del("sort")
	q.Del("direction")
	q.Del("limit")
	v.ChartQuery = q.Encode()
	h.render(w, "list", v)
}

func (h *handler) detail(w http.ResponseWriter, r *http.Request, id string) {
	if !validID(id) {
		http.NotFound(w, r)
		return
	}
	event, err := h.store.Get(r.Context(), id)
	if errors.Is(err, apilog.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	v := h.baseView(r)
	v.Title = "Request detail"
	v.Event = event
	h.render(w, "detail", v)
}

func (h *handler) chart(w http.ResponseWriter, r *http.Request, kind string) {
	if kind != "requests" && kind != "status" && kind != "sql" {
		http.NotFound(w, r)
		return
	}
	filter, err := h.parseFilter(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := h.store.Aggregate(r.Context(), filter)
	if err != nil {
		serverError(w)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if kind == "status" {
		if data.Statuses == nil {
			data.Statuses = []apilog.StatusStat{}
		}
		_ = json.NewEncoder(w).Encode(data.Statuses)
		return
	}
	if data.Days == nil {
		data.Days = []apilog.DayStat{}
	}
	if kind == "sql" {
		profiled := make([]apilog.DayStat, 0, len(data.Days))
		for _, day := range data.Days {
			if day.ProfiledCount > 0 {
				profiled = append(profiled, day)
			}
		}
		data.Days = profiled
	}
	_ = json.NewEncoder(w).Encode(data.Days)
}

func (h *handler) render(w http.ResponseWriter, name string, v view) {
	var result bytes.Buffer
	if err := h.templates.ExecuteTemplate(&result, name, v); err != nil {
		serverError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(result.Bytes())
}

func serverError(w http.ResponseWriter) {
	http.Error(w, "The log store is unavailable. Try again later.", http.StatusInternalServerError)
}

func (h *handler) parseFilter(q url.Values) (apilog.Filter, error) {
	f := apilog.Filter{Search: strings.TrimSpace(q.Get("q")), SlowThreshold: h.opts.SlowThreshold, Order: q.Get("sort"), Descending: true, Page: 1, Limit: 50}
	bad := func(field string) (apilog.Filter, error) { return f, fmt.Errorf("Invalid %s filter", field) }
	if len(f.Search) > 512 {
		return bad("search")
	}
	for _, raw := range q["method"] {
		for _, method := range strings.Split(raw, ",") {
			method = strings.ToUpper(strings.TrimSpace(method))
			if method == "" {
				continue
			}
			switch method {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT":
				f.Methods = append(f.Methods, method)
			default:
				return bad("method")
			}
		}
	}
	for _, raw := range q["status"] {
		for _, status := range strings.Split(raw, ",") {
			status = strings.TrimSpace(status)
			if status == "" {
				continue
			}
			value, err := strconv.Atoi(status)
			if err != nil || value < 100 || value > 599 {
				return bad("status")
			}
			f.StatusCodes = append(f.StatusCodes, value)
		}
	}
	if len(f.Methods) > 9 || len(f.StatusCodes) > 50 {
		return bad("selection")
	}
	for _, key := range []string{"after", "before"} {
		if value := q.Get(key); value != "" {
			date, err := time.ParseInLocation("2006-01-02", value, h.opts.Timezone)
			if err != nil {
				return bad("date")
			}
			if key == "after" {
				f.After = date.UTC()
			} else {
				f.Before = date.AddDate(0, 0, 1).UTC()
			}
		}
	}
	if !f.After.IsZero() && !f.Before.IsZero() && !f.After.Before(f.Before) {
		return bad("date range")
	}
	switch q.Get("speed") {
	case "", "all":
	case "slow":
		b := true
		f.Slow = &b
	case "fast":
		b := false
		f.Slow = &b
	default:
		return bad("speed")
	}
	switch q.Get("sql") {
	case "", "all":
	case "unprofiled":
		b := false
		f.Profiled = &b
	case "low":
		b := true
		min, max := 0, 4
		f.Profiled = &b
		f.SQLMin = &min
		f.SQLMax = &max
	case "medium":
		b := true
		min, max := 5, 9
		f.Profiled = &b
		f.SQLMin = &min
		f.SQLMax = &max
	case "high":
		b := true
		min := 10
		f.Profiled = &b
		f.SQLMin = &min
	default:
		return bad("SQL")
	}
	switch f.Order {
	case "":
		f.Order = "time"
	case "time", "method", "status", "duration", "sql":
	default:
		return bad("sort")
	}
	switch q.Get("direction") {
	case "", "desc":
	case "asc":
		f.Descending = false
	default:
		return bad("direction")
	}
	if q.Get("page") != "" {
		value, err := strconv.Atoi(q.Get("page"))
		if err != nil || value < 1 || value > 1000000 {
			return bad("page")
		}
		f.Page = value
	}
	if q.Get("limit") != "" {
		value, err := strconv.Atoi(q.Get("limit"))
		if err != nil || (value != 25 && value != 50 && value != 100) {
			return bad("page size")
		}
		f.Limit = value
	}
	return f, nil
}

func validID(id string) bool {
	return id != "" && len(id) <= 128 && !strings.ContainsAny(id, "/\\") && !strings.ContainsFunc(id, unicode.IsControl)
}

func selectedIDs(w http.ResponseWriter, r *http.Request) ([]string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := r.ParseForm(); err != nil {
		return nil, errors.New("Invalid selection")
	}
	ids := r.PostForm["id"]
	if len(ids) < 1 || len(ids) > 100 {
		return nil, errors.New("Select between 1 and 100 requests")
	}
	seen := make(map[string]bool)
	selected := make([]string, 0, len(ids))
	for _, id := range ids {
		if !validID(id) {
			return nil, errors.New("Invalid request ID")
		}
		if !seen[id] {
			selected = append(selected, id)
			seen[id] = true
		}
	}
	return selected, nil
}

func (h *handler) export(w http.ResponseWriter, r *http.Request) {
	ids, err := selectedIDs(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Load and validate the bounded selection before writing the CSV header, so a
	// missing row or store failure cannot return a misleading partial download.
	events := make([]apilog.Event, 0, len(ids))
	for _, id := range ids {
		e, err := h.store.Get(r.Context(), id)
		if errors.Is(err, apilog.ErrNotFound) {
			http.Error(w, "A selected request no longer exists", http.StatusNotFound)
			return
		}
		if err != nil {
			serverError(w)
			return
		}
		if e.ExportDisabled {
			http.Error(w, "Export is disabled by policy for a selected request", http.StatusForbidden)
			return
		}
		events = append(events, e)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="api-logs.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "time_utc", "method", "url", "route", "status", "duration_ms", "request_id", "trace_id", "client_ip", "request_headers", "response_headers", "request_body", "request_body_state", "response_body", "response_body_state", "query_count", "profiling", "version", "path", "name", "group", "handler", "protocol", "context", "panicked", "security", "request_bytes", "response_bytes", "request_content_type", "response_content_type"})
	for _, e := range events {
		queries := ""
		if e.Profile != nil && e.Profile.Instrumented {
			queries = strconv.Itoa(e.Profile.QueryCount)
		}
		row := []string{e.ID, e.Time.UTC().Format(time.RFC3339Nano), e.Method, e.URL, e.Route, strconv.Itoa(e.Status), fmt.Sprintf("%.3f", float64(e.Duration)/float64(time.Millisecond)), e.RequestID, e.TraceID, e.ClientIP, compactJSON(e.RequestHeaders), compactJSON(e.ResponseHeaders), string(e.Request.Data), e.Request.State, string(e.Response.Data), e.Response.State, queries, compactJSON(e.Profile), strconv.Itoa(e.Version), e.Path, e.Name, e.Group, e.Handler, e.Protocol, compactJSON(e.Context), strconv.FormatBool(e.Panicked), compactJSON(e.Security), strconv.FormatInt(e.Request.Bytes, 10), strconv.FormatInt(e.Response.Bytes, 10), e.Request.ContentType, e.Response.ContentType}
		for i := range row {
			row[i] = csvSafe(row[i])
		}
		if err := writer.Write(row); err != nil {
			return
		}
	}
	writer.Flush()
}

func csvSafe(value string) string {
	// Prefix dangerous leading characters, including those concealed by whitespace.
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\ufeff' })
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") || (len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + value
	}
	return value
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	ids, err := selectedIDs(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.store.Delete(r.Context(), ids); err != nil {
		serverError(w)
		return
	}
	http.Redirect(w, r, h.opts.BasePath+"/", http.StatusSeeOther)
}

func compactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "[unavailable]"
	}
	return string(data)
}
func prettyJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "[unavailable]"
	}
	return string(data)
}
