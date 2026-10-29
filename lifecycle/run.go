package lifecycle

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

// cause is what started the stop sequence.
type cause struct {
	reason    string
	code      int
	err       error  // fn's or a Go goroutine's error, or the *work.PanicError of a crash
	panicked  bool   // err is a recovered panic: the last line is process.crash
	signal    string // the signal's name when reason is ReasonSignal
	goroutine string // the Go goroutine's name when it caused the stop
}

type step struct {
	name string
	fn   func() error
}

// run is the state of the one Run of the process.
type run struct {
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
	current  string // the step running now
	failed   int    // steps that failed so far
	fnDone   chan struct{}
	stopping chan struct{} // closed by the first cause
	once     sync.Once
	cause    cause         // the first cause; a later panic replaces a non-panic cause
	forced   chan struct{} // closed by the first signal after the sequence started
	forced2  chan struct{} // closed by the second: it cuts the flush
	forces   int
	forceSig int // the code of the first forcing signal
	lastSig  int // the code of the latest forcing signal: the exit code when it cut the flush
}

type ctxKey struct{}

func newRun(log *slog.Logger, grace time.Duration) *run {
	r := &run{log: log, grace: grace, fnDone: make(chan struct{}), stopping: make(chan struct{}), forced: make(chan struct{}), forced2: make(chan struct{})}
	r.cond = sync.NewCond(&r.mu)
	ctx := logging.NewContext(context.Background(), log)
	ctx = context.WithValue(ctx, ctxKey{}, r)
	r.ctx, r.cancel = context.WithCancel(ctx)
	return r
}

// fromContext returns the run ctx belongs to; a ctx from elsewhere is a programming error.
func fromContext(ctx context.Context) *run {
	r, _ := ctx.Value(ctxKey{}).(*run)
	if r == nil {
		panic("lifecycle: ctx does not come from lifecycle.Run")
	}
	return r
}

// OnStop registers a named stop step. Steps run in reverse order of registration when the
// process stops, every one of them, each failure an ERROR line with the step's name. A step
// registered once the steps began (the Go goroutines had ended) does not run and is reported
// with a WARN line.
func OnStop(ctx context.Context, name string, fn func() error) {
	r := fromContext(ctx)
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

// Stop starts the stop sequence with reason app and exit code 0. Later calls do nothing.
func Stop(ctx context.Context) {
	fromContext(ctx).stop(cause{reason: ReasonApp})
}

// Go starts fn in a goroutine the stop sequence waits for. A panic in fn is recovered and
// written as process.crash; an error ends the process with reason error and the error's exit
// code; nil ends the goroutine quietly. A cancelled or terminated error after the stop started
// is quiet too. Once the steps began, fn does not start and a WARN line says so.
func Go(ctx context.Context, name string, fn func(ctx context.Context) error) {
	r := fromContext(ctx)
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
				r.stop(cause{reason: ReasonError, code: failure.Code(failure.Internal), err: work.NewPanicError(v), panicked: true, goroutine: name})
			}
		}()
		if err := fn(ctx); err != nil {
			r.stop(cause{reason: ReasonError, code: failure.ExitCode(err), err: err, goroutine: name})
		}
	}()
}

// stop records the first cause and starts the sequence. A later cause joins: a panic replaces a
// cause that was not one, so the run ends as process.crash with exit 1; any other error outside
// the cancelled classes is written as an ERROR line and changes nothing.
func (r *run) stop(c cause) {
	first := false
	r.once.Do(func() {
		first = true
		r.mu.Lock()
		r.cause = c
		r.mu.Unlock()
		close(r.stopping)
	})
	if first || c.err == nil || (!c.panicked && cancelled(c.err)) {
		return
	}
	if c.panicked {
		r.mu.Lock()
		upgrade := !r.cause.panicked
		if upgrade {
			r.cause = c
		}
		r.mu.Unlock()
		if upgrade {
			return
		}
	}
	attrs := []slog.Attr{slog.Any("error", c.err)}
	if c.goroutine != "" {
		attrs = append(attrs, slog.String("goroutine", c.goroutine))
	}
	body := "error after the stop started"
	if c.panicked {
		body = "panic after the stop started"
	}
	r.log.LogAttrs(r.ctx, slog.LevelError, body, attrs...)
}

// cancelled reports whether err is a cancelled or terminated failure.
func cancelled(err error) bool {
	return failure.Is(err, failure.Cancelled) || failure.Is(err, failure.Terminated)
}
