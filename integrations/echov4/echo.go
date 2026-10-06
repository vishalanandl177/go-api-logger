// Package echov4 enriches outer HTTP capture with Echo v4 route metadata.
package echov4

import (
	"github.com/labstack/echo/v4"
	apilog "github.com/vishalanandl177/go-api-logger"
)

// Metadata preserves Echo's error handling. The outer httpmw middleware records
// the response after Echo's central error handler finishes writing it.
func Metadata(group string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			apilog.SetRoute(c.Request().Context(), c.Path(), "", group)
			defer func() { apilog.SetRoute(c.Request().Context(), c.Path(), "", group) }()
			return next(c)
		}
	}
}
