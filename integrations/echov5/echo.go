// Package echov5 enriches outer HTTP capture with Echo v5 route metadata.
package echov5

import (
	"github.com/labstack/echo/v5"
	apilog "github.com/vishalanandl177/go-api-logger"
)

// Metadata preserves the framework's error handling and emits no extra event.
func Metadata(group string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			set := func() { apilog.SetRoute(c.Request().Context(), c.Path(), c.RouteInfo().Name, group) }
			set()
			defer set()
			return next(c)
		}
	}
}
