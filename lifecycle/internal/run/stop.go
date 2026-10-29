package run

import (
	"context"
	"log/slog"
	"time"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
	"github.com/AndreiD108/d108-go/work"
)

// FlushForced bounds the telemetry flush of a forced exit.
const FlushForced = 2 * time.Second

// The phases of the stop sequence, on a forced process.stop.
const (
	phaseGoroutines = "goroutines"
	phaseSteps      = "steps"
	phaseFunc       = "fn"
)

// RunFunc runs fn and turns its return or panic into the first cause.
func (r *Run) RunFunc(fn Func) {
	defer close(r.fnDone)
	defer func() {
		if v := recover(); v != nil {
			r.Stop(Cause{Reason: ReasonError, Code: failure.Code(failure.Internal), Err: work.NewPanicError(v), Panicked: true})
		}
	}()
	err := fn(r.ctx, r.log)
	reason := ReasonDone
	if err != nil {
		reason = ReasonError
	}
	r.Stop(Cause{Reason: reason, Code: failure.ExitCode(err), Err: err})
}

// Wait blocks until the first cause, runs the sequence under the grace deadline, writes the
// last line, flushes and returns the exit code. The deadline or Force forces: the sequence is
// abandoned where it is. The flush is bounded: 2 s when forced, else what is left of the
// grace with a 2 s floor (the default grace when the grace is 0); a second Force during the
// flush cuts it and the code is that signal's; the line already written keeps the code the
// sequence had.
func (r *Run) Wait() int {
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
	c, failed, forceSig, phase, step := r.cause, len(r.failed), r.forceSig, r.phase, r.current
	cut := r.forced
	if r.forces > 0 {
		cut = r.forced2
	}
	r.mu.Unlock()
	code := c.Code
	if forced && forceSig != 0 {
		code = forceSig
	}
	r.lastLine(c, code, forced, failed, phase, step)

	bound := FlushForced
	if !forced {
		grace := r.grace
		if grace <= 0 {
			grace = DefaultGrace
		}
		bound = max(grace-time.Since(begin), FlushForced)
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

// Outcome reports the run after Wait: the reason of the last line (empty for process.crash,
// which carries none) and the names of the steps that failed, in run order.
func (r *Run) Outcome() (reason string, failedSteps []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reason, append([]string(nil), r.failed...)
}

// sequence cancels ctx, waits for the Go goroutines, runs the steps in reverse order and waits
// for fn. Failed steps are recorded as they happen, so a forced line reports them.
func (r *Run) sequence() {
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
			r.failed = append(r.failed, steps[i].name)
			r.mu.Unlock()
			r.log.LogAttrs(r.ctx, slog.LevelError, "stop step failed", slog.String("step", steps[i].name), slog.Any("error", err))
		}
	}

	r.setPhase(phaseFunc, "")
	<-r.fnDone
}

// runStep runs one step; a panic in it is its error.
func (r *Run) runStep(s step) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = work.NewPanicError(v)
		}
	}()
	return s.fn()
}

func (r *Run) setPhase(phase, step string) {
	r.mu.Lock()
	r.phase, r.current = phase, step
	r.mu.Unlock()
}

// lastLine writes process.stop or process.crash, the last line of the run. A forced exit is
// always process.stop with reason forced; a panic that was the cause keeps its exception.* on
// that line. A panic that was not forced is process.crash.
func (r *Run) lastLine(c Cause, code int, forced bool, stopErrors int, phase, step string) {
	attrs := []slog.Attr{slog.Int("exit_code", code), slog.Int("stop_errors", stopErrors)}
	level := slog.LevelInfo
	event := "process.stop"
	reason := c.Reason
	switch {
	case forced:
		level = slog.LevelWarn
		reason = ReasonForced
		attrs = append(attrs, slog.String("reason", reason), slog.String("phase", phase))
		if step != "" {
			attrs = append(attrs, slog.String("step", step))
		}
		if c.Panicked || c.Reason == ReasonError {
			attrs = append(attrs, slog.String("error.type", string(failure.ClassOf(c.Err))), slog.Any("error", c.Err))
		}
	case c.Panicked:
		event, level, reason = "process.crash", logging.LevelFatal, ""
		attrs = append(attrs, slog.Any("error", c.Err))
	default:
		attrs = append(attrs, slog.String("reason", reason))
		if c.Reason == ReasonError {
			level = slog.LevelError
			attrs = append(attrs, slog.String("error.type", string(failure.ClassOf(c.Err))), slog.Any("error", c.Err))
		}
	}
	if c.Signal != "" {
		attrs = append(attrs, slog.String("signal", c.Signal))
	}
	if c.Goroutine != "" {
		attrs = append(attrs, slog.String("goroutine", c.Goroutine))
	}
	r.mu.Lock()
	r.reason = reason
	r.mu.Unlock()
	r.log.LogAttrs(r.ctx, level, "", append([]slog.Attr{logging.Event(event)}, attrs...)...)
}
