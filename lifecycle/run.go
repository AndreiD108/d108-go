package lifecycle

import (
	"context"

	"github.com/AndreiD108/d108-go/lifecycle/internal/run"
)

// The reason values of process.stop.
const (
	ReasonDone   = run.ReasonDone
	ReasonError  = run.ReasonError
	ReasonSignal = run.ReasonSignal
	ReasonApp    = run.ReasonApp
	ReasonForced = run.ReasonForced
)

// OnStop registers a named stop step. Steps run in reverse order of registration when the
// process stops, every one of them, each failure an ERROR line with the step's name. A step
// registered once the steps began (the Go goroutines had ended) does not run and is reported
// with a WARN line. ctx must come from Run (or lifecycletest.Run); another ctx panics.
func OnStop(ctx context.Context, name string, fn func() error) {
	run.FromContext(ctx).OnStop(ctx, name, fn)
}

// Stop starts the stop sequence with reason app and exit code 0. Later calls do nothing.
func Stop(ctx context.Context) {
	run.FromContext(ctx).Stop(run.Cause{Reason: ReasonApp})
}

// Go starts fn in a goroutine the stop sequence waits for. A panic in fn is recovered and
// written as process.crash; an error ends the process with reason error and the error's exit
// code; nil ends the goroutine quietly. A cancelled or terminated error after the stop started
// is quiet too. Once the steps began, fn does not start and a WARN line says so.
func Go(ctx context.Context, name string, fn func(ctx context.Context) error) {
	run.FromContext(ctx).Go(ctx, name, fn)
}
