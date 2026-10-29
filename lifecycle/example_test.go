package lifecycle_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/lifecycle"
	"github.com/AndreiD108/d108-go/work"
)

// The server of standard.md section 9.1: the database closes after the consumer, because Run
// waits for the Go goroutines before the steps; the http step makes serve return.
func Example() {
	lifecycle.Run(identity.New("api"), serve, lifecycle.Grace(20*time.Second))
}

func serve(ctx context.Context, log *slog.Logger) error {
	db, err := sql.Open("postgres", os.Getenv("DB_URL"))
	if err != nil {
		return err
	}
	lifecycle.OnStop(ctx, "db", db.Close)

	srv := &http.Server{Addr: ":8080", Handler: work.HTTP(log)(routes(db))}
	lifecycle.OnStop(ctx, "http", func() error { return srv.Shutdown(context.Background()) })

	lifecycle.Go(ctx, "consumer", func(ctx context.Context) error { return consume(ctx, db) })

	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func routes(*sql.DB) http.Handler { return http.NewServeMux() }

func consume(ctx context.Context, _ *sql.DB) error {
	<-ctx.Done()
	return ctx.Err()
}
