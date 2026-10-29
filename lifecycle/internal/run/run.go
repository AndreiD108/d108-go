// Package run is the state of one lifecycle run and its stop sequence, shared by lifecycle
// (the process owner) and lifecycletest (the in-process harness). lifecycle adds the signals,
// the heartbeat, the SDK setup and the exit; the harness adds none of them.
package run

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
	"github.com/AndreiD108/d108-go/work"
)

// The reason values of process.stop.
const (
	ReasonDone   = "done"
	ReasonError  = "error"
	ReasonSignal = "signal"
	ReasonApp    = "app"
	ReasonForced = "forced"
)

// DefaultGrace is the time the stop sequence gets before the exit is forced.
const DefaultGrace = 10 * time.Second

// Func is the app's function.
type Func = func(ctx context.Context, log *slog.Logger) error

// Options is what lifecycle's options set.
type Options struct {
	Grace     time.Duration
	Heartbeat bool
	Logging   []logging.Option
}

// Cause is what started the stop sequence.
type Cause struct {
	Reason    string
	Code      int
	Err       error  // fn's or a Go goroutine's error, or the *work.PanicError of a crash
	Panicked  bool   // Err is a recovered panic: the last line is process.crash
	Signal    string // the signal's name when Reason is ReasonSignal
	Goroutine string // the Go goroutine's name when it caused the stop
}

type step struct {
	name string
	fn   func() error
}

// Run is the state of one run.
type Run struct {
	ctx    context.Context
	cancel context.CancelFunc
	log    *slog.Logger
	grace  time.Duration
	flush  func(context.Context) error

	mu       sync.Mutex
	cond     *sync.Cond // signalled when a Go goroutine ends
	active   int        // Go goroutines running
	steps    []step
	closed   bool // the Go goroutines are drained and the steps began: OnStop and Go no longer admit
	phase    string
	current  string   // the step running now
	failed   []string // the steps that failed so far, in run order
	fnDone   chan struct{}
	stopping chan struct{} // closed by the first cause
	once     sync.Once
	cause    Cause         // the first cause; a later panic replaces a non-panic cause
	forced   chan struct{} // closed by the first signal after the sequence started
	forced2  chan struct{} // closed by the second: it cuts the flush
	forces   int
	forceSig int    // the code of the first forcing signal
	lastSig  int    // the code of the latest forcing signal: the exit code when it cut the flush
	reason   string // the reason of the last line, set by Wait
}

type ctxKey struct{}

// New makes the run: its context carries log and the run itself. flush is what Wait calls
// after the last line; the no-op is the harness's.
func New(log *slog.Logger, grace time.Duration, flush func(context.Context) error) *Run {
	r := &Run{log: log, grace: grace, flush: flush, fnDone: make(chan struct{}), stopping: make(chan struct{}), forced: make(chan struct{}), forced2: make(chan struct{})}
	r.cond = sync.NewCond(&r.mu)
	ctx := logging.NewContext(context.Background(), log)
	ctx = context.WithValue(ctx, ctxKey{}, r)
	r.ctx, r.cancel = context.WithCancel(ctx)
	return r
}

// Context is the run's context: cancelled first by the stop sequence.
func (r *Run) Context() context.Context { return r.ctx }

// Log is the run's logger.
func (r *Run) Log() *slog.Logger { return r.log }

// Stopping is closed when the first cause arrives.
func (r *Run) Stopping() <-chan struct{} { return r.stopping }

// FromContext returns the run ctx belongs to; a ctx from elsewhere is a programming error.
func FromContext(ctx context.Context) *Run {
	r, _ := ctx.Value(ctxKey{}).(*Run)
	if r == nil {
		panic("lifecycle: ctx does not come from lifecycle.Run")
	}
	return r
}

// OnStop registers a named stop step. Steps run in reverse order of registration when the
// process stops, every one of them, each failure an ERROR line with the step's name. A step
// registered once the steps began (the Go goroutines had ended) does not run and is reported
// with a WARN line.
func (r *Run) OnStop(ctx context.Context, name string, fn func() error) {
	r.mu.Lock()
	closed := r.closed
	if !closed {
		r.steps = append(r.steps, step{name, fn})
	}
	r.mu.Unlock()
	if closed {
		r.log.LogAttrs(ctx, slog.LevelWarn, "stop step registered after the steps began; it does not run", slog.String("step", name))
	}
}

// Go starts fn in a goroutine the stop sequence waits for. A panic in fn is recovered and
// written as process.crash; an error ends the run with reason error and the error's exit
// code; nil ends the goroutine quietly. A cancelled or terminated error after the stop started
// is quiet too. Once the steps began, fn does not start and a WARN line says so.
func (r *Run) Go(ctx context.Context, name string, fn func(ctx context.Context) error) {
	r.mu.Lock()
	closed := r.closed
	if !closed {
		r.active++
	}
	r.mu.Unlock()
	if closed {
		r.log.LogAttrs(ctx, slog.LevelWarn, "goroutine started after the steps began; it does not run", slog.String("goroutine", name))
		return
	}
	go func() {
		defer func() {
			r.mu.Lock()
			r.active--
			r.cond.Broadcast()
			r.mu.Unlock()
		}()
		defer func() {
			if v := recover(); v != nil {
				r.Stop(Cause{Reason: ReasonError, Code: failure.Code(failure.Internal), Err: work.NewPanicError(v), Panicked: true, Goroutine: name})
			}
		}()
		if err := fn(ctx); err != nil {
			r.Stop(Cause{Reason: ReasonError, Code: failure.ExitCode(err), Err: err, Goroutine: name})
		}
	}()
}

// Stop records the first cause and starts the sequence. A later cause joins: a panic replaces
// a cause that was not one, so the run ends as process.crash with exit 1; any other error
// outside the cancelled classes is written as an ERROR line and changes nothing.
func (r *Run) Stop(c Cause) {
	first := false
	r.once.Do(func() {
		first = true
		r.mu.Lock()
		r.cause = c
		r.mu.Unlock()
		close(r.stopping)
	})
	if first || c.Err == nil || (!c.Panicked && cancelled(c.Err)) {
		return
	}
	if c.Panicked {
		r.mu.Lock()
		upgrade := !r.cause.Panicked
		if upgrade {
			r.cause = c
		}
		r.mu.Unlock()
		if upgrade {
			return
		}
	}
	attrs := []slog.Attr{slog.Any("error", c.Err)}
	if c.Goroutine != "" {
		attrs = append(attrs, slog.String("goroutine", c.Goroutine))
	}
	body := "error after the stop started"
	if c.Panicked {
		body = "panic after the stop started"
	}
	r.log.LogAttrs(r.ctx, slog.LevelError, body, attrs...)
}

// Force ends the sequence at once; code is the forcing signal's exit code, the process exits
// with it. A second Force cuts the flush that follows the forced process.stop.
func (r *Run) Force(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forces++
	r.lastSig = code
	switch r.forces {
	case 1:
		r.forceSig = code
		close(r.forced)
	case 2:
		close(r.forced2)
	}
}

// cancelled reports whether err is a cancelled or terminated failure.
func cancelled(err error) bool {
	return failure.Is(err, failure.Cancelled) || failure.Is(err, failure.Terminated)
}
