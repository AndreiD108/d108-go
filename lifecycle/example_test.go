package lifecycle_test

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/AndreiD108/d108-go/httpserver"
	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/lifecycle"
)

// The server of standard.md section 9.1: the database closes after the consumer, because Run
// waits for the Go goroutines before the steps; the http step drains the requests and makes
// serve return.
func Example() {
	lifecycle.Run(identity.New("api"), serve, lifecycle.Grace(20*time.Second))
}

func serve(ctx context.Context, log *slog.Logger) error {
	db, err := sql.Open("postgres", os.Getenv("DB_URL"))
	if err != nil {
		return err
	}
	lifecycle.OnStop(ctx, "db", db.Close)

	srv := httpserver.New(log, routes(db), httpserver.Addr(":8080"))
	lifecycle.OnStop(ctx, "http", func() error { return srv.Shutdown(context.Background()) })

	lifecycle.Go(ctx, "consumer", func(ctx context.Context) error { return consume(ctx, db) })

	return srv.ListenAndServe(ctx)
}

func routes(*sql.DB) http.Handler { return http.NewServeMux() }

func consume(ctx context.Context, _ *sql.DB) error {
	<-ctx.Done()
	return ctx.Err()
}
