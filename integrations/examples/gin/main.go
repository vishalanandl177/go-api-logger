package main

import (
	"github.com/gin-gonic/gin"
	"github.com/vishalanandl177/go-api-logger/integrations/examples/internal/run"
	apigin "github.com/vishalanandl177/go-api-logger/integrations/gin"
)

func main() {
	r := gin.New()
	r.Use(apigin.Metadata("users"), gin.Recovery())
	r.GET("/users/:id", func(c *gin.Context) { c.JSON(200, gin.H{"id": c.Param("id")}) })
	r.POST("/users", func(c *gin.Context) {
		var body map[string]any
		if c.ShouldBindJSON(&body) != nil {
			c.JSON(400, gin.H{"error": "invalid JSON"})
			return
		}
		c.JSON(201, gin.H{"created": true})
	})
	run.Serve(r)
}
