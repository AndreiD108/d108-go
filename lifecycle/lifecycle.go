// Package lifecycle owns main: Run sets up logging, tracing and metrics from the identity,
// writes process.start, runs the app's function and takes every exit through one stop
// sequence (standard.md section 9.1). The causes are the function returning, SIGINT, SIGTERM
// or SIGHUP, Stop, a panic in the function or in a goroutine started with Go, and a Go
// goroutine returning an error. The sequence cancels ctx, waits for the Go goroutines, runs
// the OnStop steps in reverse order, waits for the function, writes process.stop or
// process.crash, flushes spans and metrics and exits with the code of the failure table.
//
//	func main() {
//		lifecycle.Run(identity.New("api"), serve, lifecycle.Grace(20*time.Second))
//	}
//
//	func serve(ctx context.Context, log *slog.Logger) error {
//		db, err := sql.Open("postgres", os.Getenv("DB_URL"))
//		if err != nil {
//			return err
//		}
//		lifecycle.OnStop(ctx, "db", db.Close)
//		srv := httpserver.New(log, routes(db), httpserver.Addr(":8080"))
//		lifecycle.OnStop(ctx, "http", func() error { return srv.Shutdown(context.Background()) })
//		lifecycle.Go(ctx, "consumer", func(ctx context.Context) error { return consumer.Run(ctx, db) })
//		return srv.ListenAndServe(ctx)
//	}
//
// Teardown is OnStop steps, not defers. os.Exit and log.Fatal inside the function skip the
// sequence; so does a panic in a goroutine started with plain go (section 6).
//
// Run makes its logger the default of log/slog, so a line written through slog.Info or a
// library that logs through slog.Default is the standard's line. A service test runs fn
// through the same sequence in-process with lifecycletest.Run.
package lifecycle

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/lifecycle/internal/run"
	"github.com/AndreiD108/d108-go/logging"
	"github.com/AndreiD108/d108-go/metrics"
	"github.com/AndreiD108/d108-go/tracing"
)

// DefaultGrace is the time the stop sequence gets before Run forces the exit.
const DefaultGrace = run.DefaultGrace

// HeartbeatInterval is the time between two process.heartbeat lines.
const HeartbeatInterval = 60 * time.Second

// Func is the app's function: it returns when the app is done or when ctx is cancelled.
type Func func(ctx context.Context, log *slog.Logger) error

// Option configures Run.
type Option func(*run.Options)

// Grace sets how long the stop sequence may take before Run forces the exit (default
// DefaultGrace). 0 or less never forces; a second signal still does.
func Grace(d time.Duration) Option { return func(o *run.Options) { o.Grace = d } }

// Heartbeat turns the process.heartbeat line on or off (default on). Commands turn it off.
func Heartbeat(on bool) Option { return func(o *run.Options) { o.Heartbeat = on } }

// Logging passes options to logging.New: KeepDays, MaxFileBytes and the rest.
func Logging(opts ...logging.Option) Option {
	return func(o *run.Options) { o.Logging = append(o.Logging, opts...) }
}

var started atomic.Bool

// Run owns main. It builds the logger, refuses to start on a bad LOG_LEVEL or LOG_OUTPUT (the
// error envelope on stderr, exit 2), turns tracing and metrics on when the environment asks,
// writes process.start, runs fn and ends the process through the stop sequence. It never
// returns. A second call in the same process panics.
func Run(id identity.Identity, fn Func, opts ...Option) {
	os.Exit(start(id, fn, opts...))
}

// start is Run without the exit; it returns the exit code.
func start(id identity.Identity, fn Func, opts ...Option) int {
	if !started.CompareAndSwap(false, true) {
		panic("lifecycle.Run: called twice in one process")
	}
	o := run.Options{Grace: DefaultGrace, Heartbeat: true}
	for _, opt := range opts {
		opt(&o)
	}
	log := logging.New(id, o.Logging...)
	if err := logging.Check(id); err != nil {
		_ = failure.Write(os.Stderr, err)
		return failure.ExitCode(err)
	}

	var flushTracing, flushMetrics func(context.Context) error
	r := run.New(log, o.Grace, func(ctx context.Context) error {
		return errors.Join(flushTracing(ctx), flushMetrics(ctx))
	})
	ctx := r.Context()
	flushTracing = tracing.Setup(ctx, id)
	flushMetrics = metrics.Setup(ctx, id)
	slog.SetDefault(log)

	exe, _ := os.Executable()
	log.LogAttrs(ctx, slog.LevelInfo, "", logging.Event("process.start"),
		slog.String("process.executable.path", exe),
		slog.Any("process.command_args", os.Args),
		slog.String("process.runtime.name", "go"),
		slog.String("process.runtime.version", strings.TrimPrefix(runtime.Version(), "go")))

	go watchSignals(r, notifySignals())
	if o.Heartbeat {
		go heartbeat(r, HeartbeatInterval)
	}
	go r.RunFunc(fn)

	return r.Wait()
}
