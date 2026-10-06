// Package run owns lifecycle management for the runnable framework examples.
package run

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
)

// Serve writes sanitized JSON logs and listens on loopback. Interrupt the process
// to stop HTTP admission, drain active requests, then flush the logger.
func Serve(router http.Handler) {
	config := apilog.DefaultConfig()
	// Make the first demo event visible promptly. Production defaults batch 50
	// events or wait 10 seconds; keep batching enabled for higher throughput.
	config.Queue.BatchSize = 1
	config.Correlation.Enabled = true
	config.Outputs = []apilog.Output{{Name: "stdout", Kind: "export", Sink: &apilog.JSONSink{Writer: os.Stdout}}}
	logger, err := apilog.New(config)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:8080", Handler: httpmw.Middleware(logger)(router), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Print("Example listening on http://127.0.0.1:8080/users/42; POST JSON to /users to see masked body capture")
	select {
	case <-ctx.Done():
	case err = <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print(err)
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := server.Shutdown(shutdown); err != nil {
		log.Print(err)
		_ = server.Close()
	}
	cancel()
	flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := logger.Shutdown(flush); err != nil {
		log.Print(err)
	}
}
