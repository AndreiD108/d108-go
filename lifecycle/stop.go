package lifecycle

import (
	"context"
	"log/slog"
	"time"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
	"github.com/AndreiD108/d108-go/work"
)

// The phases of the stop sequence, on a forced process.stop.
const (
	phaseGoroutines = "goroutines"
	phaseSteps      = "steps"
	phaseFunc       = "fn"
)

// runFunc runs fn and turns its return or panic into the first cause.
func (r *run) runFunc(fn Func) {
	defer close(r.fnDone)
	defer func() {
		if v := recover(); v != nil {
			r.stop(cause{reason: ReasonError, code: failure.Code(failure.Internal), err: work.NewPanicError(v), panicked: true})
		}
	}()
	err := fn(r.ctx, r.log)
	reason := ReasonDone
	if err != nil {
		reason = ReasonError
	}
	r.stop(cause{reason: reason, code: failure.ExitCode(err), err: err})
}

// wait blocks until the first cause, runs the sequence under the grace deadline, writes the
// last line, flushes telemetry and returns the exit code. The deadline or a signal forces: the
// sequence is abandoned where it is. The flush is bounded: 2 s when forced, else what is left
// of the grace with a 2 s floor (the default grace when Grace is 0); the next signal during
// the flush cuts it and the process exits with that signal's code; the line already written
// keeps the code the sequence had.
func (r *run) wait() int {
	<-r.stopping
	begin := time.Now()
	var deadline <-chan time.Time
	if r.grace > 0 {
		t := time.NewTimer(r.grace)
		defer t.Stop()
		deadline = t.C
	}
	done := make(chan struct{})
	go func() { r.sequence(); close(done) }()

	forced := false
	select {
	case <-done:
	case <-deadline:
		forced = true
	case <-r.forced:
		forced = true
	}

	r.mu.Lock()
	c, failed, forceSig, phase, step := r.cause, r.failed, r.forceSig, r.phase, r.current
	cut := r.forced
	if r.forces > 0 {
		cut = r.forced2
	}
	r.mu.Unlock()
	code := c.code
	if forced && forceSig != 0 {
		code = forceSig
	}
	r.lastLine(c, code, forced, failed, phase, step)

	bound := flushForced
	if !forced {
		grace := r.grace
		if grace <= 0 {
			grace = DefaultGrace
		}
		bound = max(grace-time.Since(begin), flushForced)
	}
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	flushed := make(chan struct{})
	go func() { _ = r.flush(ctx); close(flushed) }()
	select {
	case <-flushed:
	case <-cut:
		r.mu.Lock()
		code = r.lastSig
		r.mu.Unlock()
	}
	return code
}

// sequence cancels ctx, waits for the Go goroutines, runs the steps in reverse order and waits
// for fn. Failed steps are counted as they happen, so a forced line reports them.
func (r *run) sequence() {
	r.cancel()

	r.setPhase(phaseGoroutines, "")
	r.mu.Lock()
	for r.active > 0 {
		r.cond.Wait()
	}
	r.closed = true
	steps := r.steps
	r.mu.Unlock()

	for i := len(steps) - 1; i >= 0; i-- {
		r.setPhase(phaseSteps, steps[i].name)
		if err := r.runStep(steps[i]); err != nil {
			r.mu.Lock()
			r.failed++
			r.mu.Unlock()
			r.log.LogAttrs(r.ctx, slog.LevelError, "stop step failed", slog.String("step", steps[i].name), slog.Any("error", err))
		}
	}

	r.setPhase(phaseFunc, "")
	<-r.fnDone
}

// runStep runs one step; a panic in it is its error.
func (r *run) runStep(s step) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = work.NewPanicError(v)
		}
	}()
	return s.fn()
}

func (r *run) setPhase(phase, step string) {
	r.mu.Lock()
	r.phase, r.current = phase, step
	r.mu.Unlock()
}

// lastLine writes process.stop or process.crash, the last line of the run. A forced exit is
// always process.stop with reason forced; a panic that was the cause keeps its exception.* on
// that line. A panic that was not forced is process.crash.
func (r *run) lastLine(c cause, code int, forced bool, stopErrors int, phase, step string) {
	attrs := []slog.Attr{slog.Int("exit_code", code), slog.Int("stop_errors", stopErrors)}
	level := slog.LevelInfo
	event := "process.stop"
	switch {
	case forced:
		level = slog.LevelWarn
		attrs = append(attrs, slog.String("reason", ReasonForced), slog.String("phase", phase))
		if step != "" {
			attrs = append(attrs, slog.String("step", step))
		}
		if c.panicked || c.reason == ReasonError {
			attrs = append(attrs, slog.String("error.type", string(failure.ClassOf(c.err))), slog.Any("error", c.err))
		}
	case c.panicked:
		event, level = "process.crash", logging.LevelFatal
		attrs = append(attrs, slog.Any("error", c.err))
	default:
		attrs = append(attrs, slog.String("reason", c.reason))
		if c.reason == ReasonError {
			level = slog.LevelError
			attrs = append(attrs, slog.String("error.type", string(failure.ClassOf(c.err))), slog.Any("error", c.err))
		}
	}
	if c.signal != "" {
		attrs = append(attrs, slog.String("signal", c.signal))
	}
	if c.goroutine != "" {
		attrs = append(attrs, slog.String("goroutine", c.goroutine))
	}
	r.log.LogAttrs(r.ctx, level, "", append([]slog.Attr{logging.Event(event)}, attrs...)...)
}
