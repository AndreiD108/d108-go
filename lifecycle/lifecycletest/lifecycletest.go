// Package lifecycletest runs a lifecycle.Func through the real stop sequence inside a test,
// so a service test sees its OnStop steps, Go goroutines and Stop calls behave as they do
// under lifecycle.Run: ctx cancelled, the Go goroutines drained, the steps in reverse order,
// fn waited for, process.stop or process.crash as the last line. It runs no signal handling,
// no exporters, no heartbeat and no os.Exit, and it may run many times in one test binary.
//
//	res := lifecycletest.Run(t, serve)
//	if res.ExitCode != 0 || res.Reason != lifecycle.ReasonDone {
//		t.Fatalf("exit %d, reason %s", res.ExitCode, res.Reason)
//	}
//	t.Run("contract", func(t *testing.T) { contracttest.Check(t, res.Lines) })
package lifecycletest

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/lifecycle"
	"github.com/AndreiD108/d108-go/lifecycle/internal/run"
	"github.com/AndreiD108/d108-go/logging"
)

// Result is what one harness run ended with.
type Result struct {
	// ExitCode is the code lifecycle.Run would have exited with.
	ExitCode int
	// Reason is the reason of the last line: done, error, app, forced; empty on a crash.
	Reason string
	// FailedSteps names the stop steps that returned an error or panicked, in run order.
	FailedSteps []string
	// Lines holds every line the run wrote, process.start to the last line, each with its
	// newline, as contracttest.Check takes them.
	Lines [][]byte
}

// Run runs fn with the run state and the stop sequence of lifecycle.Run and returns when
// the sequence ended: when fn returned, when Stop was called, or when a Go goroutine errored
// or panicked. A panic in fn ends as process.crash with exit code 1. Grace applies, so a
// hanging step ends with reason forced; the test's own timeout is the backstop. Heartbeat
// and Logging options are ignored: the lines go to a buffer at DEBUG level through a logger
// with the identity "lifecycletest", which is also slog.Default while fn runs. Two harness
// runs must not overlap (no t.Parallel across them): slog.Default is one per process.
func Run(t testing.TB, fn lifecycle.Func, opts ...lifecycle.Option) Result {
	t.Helper()
	o := run.Options{Grace: lifecycle.DefaultGrace}
	for _, opt := range opts {
		opt(&o)
	}
	buf := &lockedBuffer{}
	log := logging.New(identity.New("lifecycletest"), logging.Writer(buf), logging.Level(slog.LevelDebug))
	prev := slog.Default()
	slog.SetDefault(log)
	defer slog.SetDefault(prev)

	r := run.New(log, o.Grace, func(context.Context) error { return nil })
	ctx := r.Context()
	log.LogAttrs(ctx, slog.LevelInfo, "", logging.Event("process.start"),
		slog.String("process.executable.path", os.Args[0]),
		slog.Any("process.command_args", os.Args),
		slog.String("process.runtime.name", "go"),
		slog.String("process.runtime.version", "test"))
	go r.RunFunc(fn)
	code := r.Wait()
	reason, failed := r.Outcome()

	var lines [][]byte
	for _, raw := range bytes.SplitAfter(buf.snapshot(), []byte("\n")) {
		if len(raw) > 0 {
			lines = append(lines, raw)
		}
	}
	return Result{ExitCode: code, Reason: reason, FailedSteps: failed, Lines: lines}
}

// lockedBuffer takes the lines of every goroutine of the run; a step abandoned by the grace
// may still write after Wait returned, so Lines is a copy taken under the lock.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) snapshot() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}
