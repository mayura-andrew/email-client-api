package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// serve runs the HTTP server until ctx is cancelled, then drains in-flight requests.
func (app *application) serve(ctx context.Context) error {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", app.config.port),
		Handler:           app.routes(),
		IdleTimeout:       time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
		// CSV and template uploads need more than a few seconds on slow links.
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	shutdownError := make(chan error, 1)

	go func() {
		<-ctx.Done()

		app.logger.PrintInfo("shutting down server", map[string]string{
			"addr": srv.Addr,
		})

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		shutdownError <- srv.Shutdown(shutdownCtx)
	}()

	app.logger.PrintInfo("starting server", map[string]string{
		"addr": srv.Addr,
		"env":  app.config.env,
	})

	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	err = <-shutdownError
	if err != nil {
		return err
	}

	app.logger.PrintInfo("stopped server", map[string]string{
		"addr": srv.Addr,
	})
	return nil
}
