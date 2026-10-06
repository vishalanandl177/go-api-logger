package dashboard

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
)

type memoryStore struct {
	events                              []apilog.Event
	filter                              apilog.Filter
	analytics                           apilog.Analytics
	deleted                             []string
	listCalls, aggregateCalls, getCalls int
	err                                 error
	missing                             map[string]bool
}

func (*memoryStore) WriteBatch(context.Context, []apilog.Event) error { return nil }
func (*memoryStore) Migrate(context.Context) error                    { return nil }
func (*memoryStore) Check(context.Context) error                      { return nil }
func (s *memoryStore) List(_ context.Context, f apilog.Filter) (apilog.Page, error) {
	s.listCalls++
	s.filter = f
	return apilog.Page{Events: s.events, Total: 150}, s.err
}
func (s *memoryStore) Get(_ context.Context, id string) (apilog.Event, error) {
	s.getCalls++
	if s.err != nil {
		return apilog.Event{}, s.err
	}
	if s.missing[id] {
		return apilog.Event{}, apilog.ErrNotFound
	}
	for _, e := range s.events {
		if e.ID == id {
			return e, nil
		}
	}
	return apilog.Event{}, apilog.ErrNotFound
}
func (s *memoryStore) Aggregate(_ context.Context, f apilog.Filter) (apilog.Analytics, error) {
	s.aggregateCalls++
	s.filter = f
	return s.analytics, s.err
}
func (s *memoryStore) Delete(_ context.Context, ids []string) (int64, error) {
	s.deleted = append([]string{}, ids...)
	return int64(len(ids)), s.err
}
func (*memoryStore) Prune(context.Context, apilog.PruneOptions) (apilog.PruneResult, error) {
	return apilog.PruneResult{}, nil
}

func sampleEvent() apilog.Event {
	return apilog.Event{ID: "first", Time: time.Date(2026, 10, 6, 10, 15, 0, 0, time.UTC), Method: "GET", Path: "/orders/42", URL: "https://example.test/orders/42", Route: "/orders/{id}", Status: 200, Duration: 42 * time.Millisecond, Request: apilog.Body{State: "captured", Data: json.RawMessage(`{"token":"***FILTERED***"}`)}, Response: apilog.Body{State: "captured", Data: json.RawMessage(`{"ok":true}`)}, Profile: &apilog.Profile{Instrumented: true, QueryCount: 3, Diagnostics: []string{"Review repeated queries"}}}
}

func makeHandler(t *testing.T, s *memoryStore, options Options) http.Handler {
	t.Helper()
	if options.Authorize == nil {
		options.Authorize = func(*http.Request, Action) bool { return true }
	}
	h, err := New(s, options)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func request(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://example.com")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestConstructionRequiresAuthorizationAndCleanMount(t *testing.T) {
	if _, err := New(&memoryStore{}, Options{}); err == nil {
		t.Fatal("missing authorization accepted")
	}
	if _, err := New(nil, Options{Authorize: func(*http.Request, Action) bool { return true }}); err == nil {
		t.Fatal("missing store accepted")
	}
	for _, base := range []string{"relative", "//outside", "/logs/../secret", "/logs?x", "/logs#x", "/logs%2Fsecret", "/logs\\secret"} {
		if _, err := New(&memoryStore{}, Options{BasePath: base, Authorize: func(*http.Request, Action) bool { return true }}); err == nil {
			t.Errorf("invalid BasePath %q accepted", base)
		}
	}
}

func TestListUsesFiltersWithoutPreloadingCharts(t *testing.T) {
	s := &memoryStore{events: []apilog.Event{sampleEvent()}}
	h := makeHandler(t, s, Options{BasePath: "/ops/logs", Timezone: time.FixedZone("IST", 19800)})
	query := "q=orders&method=GET&status=200%2C404&after=2026-10-01&before=2026-10-06&speed=slow&sql=medium&sort=duration&direction=asc&page=2&limit=25"
	w := request(h, "GET", "/ops/logs/?"+query, "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	f := s.filter
	if f.Search != "orders" || !reflect.DeepEqual(f.Methods, []string{"GET"}) || !reflect.DeepEqual(f.StatusCodes, []int{200, 404}) || f.Order != "duration" || f.Descending || f.Page != 2 || f.Limit != 25 || f.Slow == nil || !*f.Slow || f.SQLMin == nil || *f.SQLMin != 5 || *f.SQLMax != 9 || f.Profiled == nil || !*f.Profiled {
		t.Fatalf("wrong filter: %#v", f)
	}
	if want := time.Date(2026, 9, 30, 18, 30, 0, 0, time.UTC); !f.After.Equal(want) {
		t.Fatalf("after=%v want %v", f.After, want)
	}
	if want := time.Date(2026, 10, 6, 18, 30, 0, 0, time.UTC); !f.Before.Equal(want) {
		t.Fatalf("before=%v want %v", f.Before, want)
	}
	if s.aggregateCalls != 0 {
		t.Fatal("charts were eagerly computed")
	}
	for _, part := range []string{"/ops/logs/events/first", "06 Oct 2026, 15:45:00.000", "page=1", "page=3", "/ops/logs/assets/dashboard.css", "name=\"method\"", "value=\"GET\" selected"} {
		if !strings.Contains(w.Body.String(), part) {
			t.Errorf("missing %q", part)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("missing privacy headers")
	}
}

func TestListDefaultAndInvalidFilters(t *testing.T) {
	s := &memoryStore{}
	h := makeHandler(t, s, Options{})
	if w := request(h, "GET", "/api-logs/", ""); w.Code != 200 {
		t.Fatalf("default list: %d %s", w.Code, w.Body.String())
	}
	if !s.filter.Descending || s.filter.Order != "time" || s.filter.Page != 1 || s.filter.Limit != 50 {
		t.Fatalf("wrong defaults: %#v", s.filter)
	}
	for _, query := range []string{"page=0", "page=1000001", "limit=500", "sort=raw_sql", "direction=sideways", "status=999", "method=UNKNOWN", "after=garbage", "after=2026-10-06&before=2026-10-01", "speed=anything", "sql=anything", "q=" + strings.Repeat("x", 513)} {
		if w := request(h, "GET", "/api-logs/?"+query, ""); w.Code != 400 {
			t.Errorf("query %q: status %d", query, w.Code)
		}
	}
}

func TestChartFiltersAndMissingSQLCoverage(t *testing.T) {
	s := &memoryStore{analytics: apilog.Analytics{Days: []apilog.DayStat{{Day: "2026-10-05", Count: 3}, {Day: "2026-10-06", Count: 4, ProfiledCount: 2, AverageSQL: 2.5}}, Statuses: []apilog.StatusStat{{Status: 200, Count: 7}}}}
	h := makeHandler(t, s, Options{})
	w := request(h, "GET", "/api-logs/charts/sql?q=orders&sql=high&status=200", "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("chart: %d %s", w.Code, w.Body.String())
	}
	var days []apilog.DayStat
	if err := json.Unmarshal(w.Body.Bytes(), &days); err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0].Day != "2026-10-06" || days[0].AverageSQL != 2.5 {
		t.Fatalf("wrong SQL series: %+v", days)
	}
	if s.filter.Search != "orders" || s.filter.SQLMin == nil || *s.filter.SQLMin != 10 || s.filter.StatusCodes[0] != 200 {
		t.Fatalf("chart lost filters: %#v", s.filter)
	}
	w = request(h, "GET", "/api-logs/charts/requests", "")
	if err := json.Unmarshal(w.Body.Bytes(), &days); err != nil || len(days) != 2 {
		t.Fatalf("requests omitted unprofiled day: %s", w.Body.String())
	}
	w = request(h, "GET", "/api-logs/charts/status", "")
	if !strings.Contains(w.Body.String(), `"status":200`) {
		t.Fatalf("status chart: %s", w.Body.String())
	}
	if w := request(h, "GET", "/api-logs/charts/unknown", ""); w.Code != 404 {
		t.Fatal("unknown chart accepted")
	}
}

func TestAuthorizationAndCSRFGuardEveryAction(t *testing.T) {
	s := &memoryStore{events: []apilog.Event{sampleEvent()}}
	denyAll := makeHandler(t, s, Options{Authorize: func(*http.Request, Action) bool { return false }})
	for _, path := range []string{"/api-logs/", "/api-logs/events/first", "/api-logs/charts/requests", "/api-logs/assets/dashboard.js"} {
		if w := request(denyAll, "GET", path, ""); w.Code != 403 {
			t.Errorf("unprotected %s", path)
		}
	}
	if s.listCalls != 0 || s.getCalls != 0 || s.aggregateCalls != 0 {
		t.Fatal("unauthorized read reached store")
	}
	viewOnly := makeHandler(t, s, Options{Authorize: func(_ *http.Request, a Action) bool { return a == View }})
	for _, action := range []string{"export", "delete"} {
		if w := request(viewOnly, "POST", "/api-logs/"+action, "id=first"); w.Code != 403 {
			t.Errorf("unprotected %s", action)
		}
	}
	h := makeHandler(t, s, Options{})
	for _, action := range []string{"export", "delete"} {
		for _, headers := range []map[string]string{{"Origin": "https://attacker.test"}, {"Sec-Fetch-Site": "cross-site"}, {"Sec-Fetch-Site": "same-site"}, {"Origin": "null"}} {
			r := httptest.NewRequest("POST", "/api-logs/"+action, strings.NewReader("id=first"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for key, value := range headers {
				r.Header.Set(key, value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Errorf("CSRF accepted for %s with %#v: %d", action, headers, w.Code)
			}
		}
		if w := request(h, "GET", "/api-logs/"+action+"?id=first", ""); w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Errorf("GET %s allowed", action)
		}
	}
	if len(s.deleted) != 0 {
		t.Fatal("unauthorized delete reached store")
	}
}

func TestDetailEscapesUntrustedLogContent(t *testing.T) {
	e := sampleEvent()
	e.Path = `/orders/<script>alert(1)</script>`
	e.URL = `javascript:alert(1)`
	e.Profile.Diagnostics = []string{`<img src=x onerror=alert(1)>`}
	e.Request.Data = json.RawMessage(`{"name":"</pre><script>alert(1)</script>"}`)
	s := &memoryStore{events: []apilog.Event{e}}
	h := makeHandler(t, s, Options{})
	w := request(h, "GET", "/api-logs/events/first", "")
	if w.Code != 200 {
		t.Fatalf("detail failed: %s", w.Body.String())
	}
	for _, unsafe := range []string{"<script>alert(1)</script>", "<img src=x onerror=alert(1)>", `href="javascript:alert(1)"`} {
		if strings.Contains(w.Body.String(), unsafe) {
			t.Errorf("unescaped content %q", unsafe)
		}
	}
	if !strings.Contains(w.Body.String(), "&lt;img") || !strings.Contains(w.Body.String(), "Performance profile") {
		t.Fatal("detail missing escaped diagnostic or profile")
	}
	if w := request(h, "GET", "/api-logs/events/missing", ""); w.Code != 404 {
		t.Fatal("missing request not 404")
	}
}

func TestCSVPreservesColumnsAndNeutralizesFormulas(t *testing.T) {
	e := sampleEvent()
	e.URL = " \t=HYPERLINK(\"https://attacker.test\")"
	e.RequestID = "@SUM(1+1)"
	e.ClientIP = "+1"
	e.TraceID = "-2"
	e.Path = "/original"
	s := &memoryStore{events: []apilog.Event{e}}
	h := makeHandler(t, s, Options{})
	w := request(h, "POST", "/api-logs/export", "id=first&id=first")
	if w.Code != 200 || w.Header().Get("Content-Disposition") != `attachment; filename="api-logs.csv"` {
		t.Fatalf("export failed: %d %s", w.Code, w.Body.String())
	}
	rows, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("selection wasn't deduplicated: %d rows", len(rows))
	}
	if len(rows[0]) != len(rows[1]) || rows[1][3] != "'"+e.URL || rows[1][7] != "'"+e.RequestID || rows[1][8] != "'"+e.TraceID || rows[1][9] != "'"+e.ClientIP || rows[1][16] != "3" {
		t.Fatalf("unsafe CSV row: %#v", rows[1])
	}
	if s.getCalls != 1 {
		t.Fatalf("duplicate selected ID queried %d times", s.getCalls)
	}
	for _, unsafe := range []string{"=1+1", "+1", "-1", "@SUM(1)", "\ttext", "\rtext", "\ntext", "  =1", "\u2003@cmd", "\ufeff=1", "\x00=1"} {
		if !strings.HasPrefix(csvSafe(unsafe), "'") {
			t.Errorf("formula not escaped: %q", unsafe)
		}
	}
}

func TestCSVEnforcesPersistedExportPolicyAndNoPartialFailure(t *testing.T) {
	first := sampleEvent()
	second := first
	second.ID = "restricted"
	second.ExportDisabled = true
	s := &memoryStore{events: []apilog.Event{first, second}}
	h := makeHandler(t, s, Options{})
	w := request(h, "POST", "/api-logs/export", "id=first&id=restricted")
	if w.Code != 403 || strings.Contains(w.Header().Get("Content-Type"), "csv") || strings.Contains(w.Body.String(), "https://example.test") {
		t.Fatalf("policy leak: %d %s", w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api-logs/export", "id=first&id=missing")
	if w.Code != 404 || strings.Contains(w.Header().Get("Content-Type"), "csv") {
		t.Fatal("partial export sent before validation")
	}
	w = request(h, "GET", "/api-logs/events/restricted", "")
	if strings.Contains(w.Body.String(), ">Export request</button>") {
		t.Fatal("restricted detail offers export")
	}
}

func TestDeleteSelectionBoundsAndSafeRedirect(t *testing.T) {
	s := &memoryStore{}
	h := makeHandler(t, s, Options{BasePath: "/ops/logs"})
	w := request(h, "POST", "/ops/logs/delete", "id=first&id=second&id=first&return=https://attacker.test")
	if w.Code != 303 || w.Header().Get("Location") != "/ops/logs/" || !reflect.DeepEqual(s.deleted, []string{"first", "second"}) {
		t.Fatalf("delete failed: %d %q %#v", w.Code, w.Header().Get("Location"), s.deleted)
	}
	for _, body := range []string{"", "id=", strings.Repeat("id=first&", 101), "id=a%2Fb", "id=" + strings.Repeat("a", 129), "id=" + strings.Repeat("a", 40000)} {
		if w := request(h, "POST", "/ops/logs/delete", body); w.Code != 400 {
			t.Errorf("invalid selection returned %d", w.Code)
		}
	}
}

func TestStoreErrorsAreNotLeaked(t *testing.T) {
	s := &memoryStore{err: errors.New("password=database-secret SQL SELECT private")}
	h := makeHandler(t, s, Options{})
	for _, path := range []string{"/api-logs/", "/api-logs/events/first", "/api-logs/charts/requests"} {
		w := request(h, "GET", path, "")
		if w.Code != 500 || strings.Contains(w.Body.String(), "database-secret") {
			t.Errorf("error leaked at %s: %s", path, w.Body.String())
		}
	}
	for _, action := range []string{"delete", "export"} {
		w := request(h, "POST", "/api-logs/"+action, "id=first")
		if w.Code != 500 || strings.Contains(w.Body.String(), "database-secret") {
			t.Errorf("error leaked at %s", action)
		}
	}
}

func TestAssetsAndBasePathRouting(t *testing.T) {
	h := makeHandler(t, &memoryStore{}, Options{BasePath: "/ops/logs/"})
	for _, asset := range []string{"dashboard.css", "dashboard.js"} {
		w := request(h, "GET", "/ops/logs/assets/"+asset, "")
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Errorf("asset %s failed: %d", asset, w.Code)
		}
	}
	for _, path := range []string{"/other/", "/ops/logs/assets/", "/ops/logs/assets/private.txt", "/ops/logs/events/a/b"} {
		if w := request(h, "GET", path, ""); w.Code != 404 {
			t.Errorf("unexpected route %s: %d", path, w.Code)
		}
	}
	w := request(h, "GET", "/ops/logs?q=test", "")
	if w.Code != 303 || w.Header().Get("Location") != "/ops/logs/?q=test" {
		t.Fatal("mount redirect lost query")
	}
	h = makeHandler(t, &memoryStore{}, Options{BasePath: "/"})
	if w := request(h, "GET", "/", ""); w.Code != 200 {
		t.Fatalf("root mount failed %d", w.Code)
	}
}

func TestApplicationIdentityContextReachesAuthorizer(t *testing.T) {
	type identityKey struct{}
	h := makeHandler(t, &memoryStore{}, Options{Authorize: func(r *http.Request, _ Action) bool { return r.Context().Value(identityKey{}) == "operator" }})
	r := httptest.NewRequest("GET", "/api-logs/", nil).WithContext(context.WithValue(context.Background(), identityKey{}, "operator"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("application identity context lost")
	}
}

func TestQueryHelpersAcceptRepeatedMethodsAndEscapeSearch(t *testing.T) {
	s := &memoryStore{}
	h := makeHandler(t, s, Options{})
	q := url.Values{"method": {"GET", "POST"}, "q": {`"><script>alert(1)</script>`}}
	w := request(h, "GET", "/api-logs/?"+q.Encode(), "")
	if w.Code != 200 || len(s.filter.Methods) != 2 || strings.Contains(w.Body.String(), `"><script>alert(1)</script>`) {
		t.Fatalf("query failed: %d %s", w.Code, w.Body.String())
	}
}

func TestEmptyChartIsArray(t *testing.T) {
	h := makeHandler(t, &memoryStore{}, Options{})
	for _, kind := range []string{"requests", "sql", "status"} {
		w := request(h, "GET", "/api-logs/charts/"+kind, "")
		data, _ := io.ReadAll(w.Body)
		if strings.TrimSpace(string(data)) != "[]" {
			t.Errorf("%s: %s", kind, data)
		}
	}
}

func TestUnavailableSQLAndUnsentStatusAreExplicit(t *testing.T) {
	e := sampleEvent()
	e.Status = 0
	e.Profile = &apilog.Profile{Instrumented: false}
	h := makeHandler(t, &memoryStore{events: []apilog.Event{e}}, Options{})
	w := request(h, "GET", "/api-logs/events/first", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Not sent") || strings.Count(w.Body.String(), "Not tracked") < 4 {
		t.Fatalf("unavailable measurements shown as values: %s", w.Body.String())
	}
	w = request(h, "GET", "/api-logs/?sort=duration&direction=asc", "")
	if !strings.Contains(w.Body.String(), `aria-sort="ascending"`) || !strings.Contains(w.Body.String(), `>↑</span>`) {
		t.Fatal("sort direction not exposed")
	}
}

func TestDashboardAutomaticallyExcludesItsOwnRequests(t *testing.T) {
	for _, target := range []string{"/custom/inspect/", "/custom/inspect/charts/requests", "/custom/inspect/events/first", "/custom/inspect/assets/dashboard.css"} {
		t.Run(target, func(t *testing.T) {
			persisted := make(chan apilog.Event, 4)
			config := apilog.DefaultConfig()
			config.Outputs = []apilog.Output{{Name: "test", Kind: "storage", Sink: apilog.SinkFunc(func(_ context.Context, events []apilog.Event) error {
				for _, e := range events {
					persisted <- e
				}
				return nil
			})}}
			logger, err := apilog.New(config)
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Shutdown(context.Background())
			h := makeHandler(t, &memoryStore{events: []apilog.Event{sampleEvent()}}, Options{BasePath: "/custom/inspect"})
			w := request(httpmw.Middleware(logger)(h), "GET", target, "")
			if w.Code != 200 {
				t.Fatalf("dashboard request failed: %d %s", w.Code, w.Body.String())
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := logger.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case e := <-persisted:
				t.Fatalf("dashboard request was persisted: %s", e.URL)
			default:
			}
		})
	}
}
