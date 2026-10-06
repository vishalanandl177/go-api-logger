package main

import (
	"github.com/labstack/echo/v5"
	apimetadata "github.com/vishalanandl177/go-api-logger/integrations/echov5"
	"github.com/vishalanandl177/go-api-logger/integrations/examples/internal/run"
)

func main() {
	r := echo.New()
	r.Use(apimetadata.Metadata("users"))
	r.GET("/users/:id", func(c *echo.Context) error { return c.JSON(200, map[string]string{"id": c.Param("id")}) })
	r.GET("/error", func(c *echo.Context) error { return echo.NewHTTPError(422, "example validation error") })
	run.Serve(r)
}
