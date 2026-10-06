package main

import (
	"net/http"

	"github.com/labstack/echo/v5"
	apimetadata "github.com/vishalanandl177/go-api-logger/integrations/echov5"
	"github.com/vishalanandl177/go-api-logger/integrations/examples/internal/run"
)

func main() {
	run.Serve(newRouter())
}

func newRouter() http.Handler {
	r := echo.New()
	r.Use(apimetadata.Metadata("users"))
	r.GET("/users/:id", func(c *echo.Context) error { return c.JSON(200, map[string]string{"id": c.Param("id")}) })
	r.POST("/users", func(c *echo.Context) error {
		var body map[string]any
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid JSON")
		}
		return c.JSON(http.StatusCreated, map[string]bool{"created": true})
	})
	r.GET("/error", func(c *echo.Context) error { return echo.NewHTTPError(422, "example validation error") })
	return r
}
