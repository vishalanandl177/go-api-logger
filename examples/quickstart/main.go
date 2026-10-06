// Command quickstart logs a small net/http JSON API to standard output.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := apilog.DefaultConfig()
	cfg.Queue.FlushInterval = time.Second // Show the first log promptly in this demo.
	cfg.Correlation.Enabled = true
	cfg.Outputs = []apilog.Output{{
		Name: "stdout", Kind: "export",
		Sink: &apilog.JSONSink{Writer: os.Stdout},
	}}
	logger, err := apilog.New(cfg)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := logger.Shutdown(ctx); err != nil {
			log.Printf("logger shutdown: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", apilog.RequestID(r.Context()))
		var input struct {
			Name  string `json:"name"`
			Token string `json:"token"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "provide one JSON object"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Hello, " + input.Name})
	})
	server := &http.Server{
		Addr: "127.0.0.1:8080", Handler: httpmw.Middleware(logger)(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	serverError := make(chan error, 1)
	go func() { serverError <- server.ListenAndServe() }()
	log.Print("POST JSON to http://127.0.0.1:8080/hello; Ctrl+C to stop")
	select {
	case <-stop.Done():
	case err = <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	// Drain active handlers even when an accept/listener error stopped serving.
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := server.Shutdown(ctx)
	done()
	if shutdownErr != nil {
		_ = server.Close()
	}
	return errors.Join(err, shutdownErr)
}
