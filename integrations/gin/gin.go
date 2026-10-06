// Package gin enriches outer HTTP capture with Gin route metadata.
package gin

import (
	"github.com/gin-gonic/gin"
	apilog "github.com/vishalanandl177/go-api-logger"
)

// Metadata does not emit logs. Wrap the engine with httpmw.Middleware so capture
// observes final responses, including framework error and recovery handlers.
func Metadata(group string) gin.HandlerFunc {
	return func(c *gin.Context) {
		set := func() {
			apilog.SetRoute(c.Request.Context(), c.FullPath(), "", group)
			apilog.SetHandler(c.Request.Context(), c.HandlerName())
		}
		set()
		defer set()
		c.Next()
	}
}
