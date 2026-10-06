package main

import (
	"testing"

	"github.com/vishalanandl177/go-api-logger/integrations/examples/internal/exampletest"
)

func TestExample(t *testing.T) {
	exampletest.Check(t, newRouter(), "/users/{id}", "/users")
}
