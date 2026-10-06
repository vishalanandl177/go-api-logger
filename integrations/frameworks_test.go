package integrations_test

import (
	gin "github.com/gin-gonic/gin"
	chi "github.com/go-chi/chi/v5"
	echo4 "github.com/labstack/echo/v4"
	echo5 "github.com/labstack/echo/v5"
	"github.com/vishalanandl177/go-api-logger/httpmw"
	apichi "github.com/vishalanandl177/go-api-logger/integrations/chi"
	api4 "github.com/vishalanandl177/go-api-logger/integrations/echov4"
	api5 "github.com/vishalanandl177/go-api-logger/integrations/echov5"
	apigin "github.com/vishalanandl177/go-api-logger/integrations/gin"
	"github.com/vishalanandl177/go-api-logger/integrations/internal/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFrameworkFinalResponseAndRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := gin.New()
	g.Use(apigin.Metadata("users"))
	g.GET("/users/:id", func(c *gin.Context) { c.JSON(422, gin.H{"error": "invalid"}) })
	c := chi.NewRouter()
	c.Use(apichi.Metadata("users"))
	c.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "invalid", 422) })
	e4 := echo4.New()
	e4.Use(api4.Metadata("users"))
	e4.GET("/users/:id", func(c echo4.Context) error { return echo4.NewHTTPError(422, "invalid") })
	e5 := echo5.New()
	e5.Use(api5.Metadata("users"))
	e5.GET("/users/:id", func(c *echo5.Context) error { return echo5.NewHTTPError(422, "invalid") })
	for _, tt := range []struct {
		name    string
		handler http.Handler
		route   string
	}{{"gin", g, "/users/:id"}, {"chi", c, "/users/{id}"}, {"echo4", e4, "/users/:id"}, {"echo5", e5, "/users/:id"}} {
		t.Run(tt.name, func(t *testing.T) {
			l, o := testutil.Logger(t)
			w := httptest.NewRecorder()
			httpmw.Middleware(l)(tt.handler).ServeHTTP(w, httptest.NewRequest("GET", "/users/secret-id", nil))
			e := o.Events()
			if len(e) != 1 {
				t.Fatalf("events=%d", len(e))
			}
			if w.Code != 422 || e[0].Status != 422 || e[0].Route != tt.route || e[0].Group != "users" {
				t.Fatalf("response=%d event=%+v", w.Code, e[0])
			}
			if e[0].Profile == nil || e[0].Profile.Instrumented {
				t.Fatal("unconfigured SQL must be reported as missing instrumentation")
			}
		})
	}
}

func TestGinRecoveryAndNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := gin.New()
	g.Use(apigin.Metadata("api"), gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) { c.AbortWithStatus(500) }))
	g.GET("/panic", func(*gin.Context) { panic("secret") })
	l, o := testutil.Logger(t)
	h := httpmw.Middleware(l)(g)
	for _, path := range []string{"/panic", "/missing"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	e := o.Events()
	if len(e) != 2 || e[0].Status != 500 || e[1].Status != 404 {
		t.Fatalf("events=%+v", e)
	}
}
