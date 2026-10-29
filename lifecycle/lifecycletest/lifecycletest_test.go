package lifecycletest_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/AndreiD108/d108/logcheck"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/lifecycle"
	"github.com/AndreiD108/d108-go/lifecycle/lifecycletest"
)

type line map[string]any

func (l line) str(key string) string {
	s, _ := l[key].(string)
	return s
}

// decode checks every line with logcheck and decodes it.
func decode(t *testing.T, res lifecycletest.Result) []line {
	t.Helper()
	var out []line
	for _, raw := range res.Lines {
		if problems := logcheck.Check(raw); problems != nil {
			t.Errorf("logcheck: %+v\n%s", problems, raw)
		}
		var l line
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatalf("bad line %q: %v", raw, err)
		}
		out = append(out, l)
	}
	return out
}

func events(lines []line) []string {
	var names []string
	for _, l := range lines {
		if e := l.str("event_name"); e != "" {
			names = append(names, e)
		}
	}
	return names
}

func bodies(lines []line) []string {
	var out []string
	for _, l := range lines {
		if b := l.str("body"); b != "" {
			out = append(out, b)
		}
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func step(ctx context.Context, log *slog.Logger, name string, err error) {
	lifecycle.OnStop(ctx, name, func() error {
		log.InfoContext(ctx, "step "+name)
		return err
	})
}

func TestStopOrder(t *testing.T) {
	prev := slog.Default()
	var mu sync.Mutex
	var order []string
	res := lifecycletest.Run(t, func(ctx context.Context, log *slog.Logger) error {
		if slog.Default() != log {
			t.Error("slog.Default inside fn is not the run's logger")
		}
		step(ctx, log, "store", nil)
		step(ctx, log, "http", nil)
		lifecycle.Go(ctx, "worker", func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(20 * time.Millisecond)
			log.InfoContext(ctx, "worker drained")
			return ctx.Err()
		})
		lifecycle.OnStop(ctx, "fn-wait", func() error {
			mu.Lock()
			order = append(order, "fn-wait")
			mu.Unlock()
			return nil
		})
		slog.Info("through the default logger")
		lifecycle.Stop(ctx)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		order = append(order, "fn-returned")
		mu.Unlock()
		return ctx.Err()
	})
	if slog.Default() != prev {
		t.Error("the default logger was not restored")
	}
	if res.ExitCode != 0 || res.Reason != lifecycle.ReasonApp || len(res.FailedSteps) != 0 {
		t.Errorf("exit %d reason %q failed %v", res.ExitCode, res.Reason, res.FailedSteps)
	}
	lines := decode(t, res)
	if got := events(lines); !equal(got, []string{"process.start", "process.stop"}) {
		t.Errorf("events %v", got)
	}
	if got := bodies(lines); !equal(got, []string{"through the default logger", "worker drained", "step http", "step store"}) {
		t.Errorf("bodies %v, want the Go drain, then the steps reversed", got)
	}
	if !equal(order, []string{"fn-wait", "fn-returned"}) {
		t.Errorf("order %v, want the steps before fn returned", order)
	}
	last := lines[len(lines)-1]
	if last.str("reason") != "app" || last.str("service.name") != "lifecycletest" {
		t.Errorf("last line %v", last)
	}
}

func TestFailingStepAndSecondRun(t *testing.T) {
	res := lifecycletest.Run(t, func(ctx context.Context, log *slog.Logger) error {
		step(ctx, log, "a", nil)
		step(ctx, log, "b", errors.New("close failed"))
		lifecycle.OnStop(ctx, "c", func() error { panic("step boom") })
		return failure.New(failure.NotFound, "no index", "run index rebuild")
	})
	if res.ExitCode != failure.Code(failure.NotFound) || res.Reason != lifecycle.ReasonError || !equal(res.FailedSteps, []string{"c", "b"}) {
		t.Errorf("exit %d reason %q failed %v", res.ExitCode, res.Reason, res.FailedSteps)
	}
	lines := decode(t, res)
	last := lines[len(lines)-1]
	if last.str("event_name") != "process.stop" || last.str("error.type") != "not_found" || last["stop_errors"] != 2.0 {
		t.Errorf("last line %v", last)
	}

	res = lifecycletest.Run(t, func(ctx context.Context, log *slog.Logger) error {
		step(ctx, log, "only", nil)
		return nil
	})
	if res.ExitCode != 0 || res.Reason != lifecycle.ReasonDone || len(res.FailedSteps) != 0 {
		t.Errorf("second run: exit %d reason %q failed %v", res.ExitCode, res.Reason, res.FailedSteps)
	}
	if got := bodies(decode(t, res)); !equal(got, []string{"step only"}) {
		t.Errorf("second run bodies %v", got)
	}
}

func TestPanicInGo(t *testing.T) {
	res := lifecycletest.Run(t, func(ctx context.Context, log *slog.Logger) error {
		step(ctx, log, "a", nil)
		lifecycle.Go(ctx, "worker", func(ctx context.Context) error { panic("boom") })
		<-ctx.Done()
		return ctx.Err()
	})
	if res.ExitCode != 1 || res.Reason != "" || len(res.FailedSteps) != 0 {
		t.Errorf("exit %d reason %q failed %v", res.ExitCode, res.Reason, res.FailedSteps)
	}
	lines := decode(t, res)
	last := lines[len(lines)-1]
	if last.str("event_name") != "process.crash" || last.str("goroutine") != "worker" || last.str("exception.message") != "panic: boom" {
		t.Errorf("last line %v", last)
	}
	if got := bodies(lines); !equal(got, []string{"step a"}) {
		t.Errorf("bodies %v", got)
	}
}

func TestPanicInFn(t *testing.T) {
	res := lifecycletest.Run(t, func(ctx context.Context, log *slog.Logger) error {
		panic("fn boom")
	})
	if res.ExitCode != 1 || res.Reason != "" {
		t.Errorf("exit %d reason %q failed %v", res.ExitCode, res.Reason, res.FailedSteps)
	}
	lines := decode(t, res)
	if last := lines[len(lines)-1]; last.str("event_name") != "process.crash" {
		t.Errorf("last line %v", last)
	}
}

func TestGraceForcesAHangingStep(t *testing.T) {
	res := lifecycletest.Run(t, func(ctx context.Context, log *slog.Logger) error {
		lifecycle.OnStop(ctx, "slow", func() error { select {} })
		return nil
	}, lifecycle.Grace(100*time.Millisecond))
	if res.Reason != lifecycle.ReasonForced || res.ExitCode != 0 {
		t.Errorf("exit %d reason %q failed %v", res.ExitCode, res.Reason, res.FailedSteps)
	}
	lines := decode(t, res)
	if last := lines[len(lines)-1]; last.str("reason") != "forced" || last.str("step") != "slow" {
		t.Errorf("last line %v", last)
	}
}

// Eight Go goroutines log at once while a step hangs past the grace and logs after the
// harness returned; Lines is whole and stable.
func TestConcurrentLinesAndLateWriter(t *testing.T) {
	released := make(chan struct{})
	res := lifecycletest.Run(t, func(ctx context.Context, log *slog.Logger) error {
		lifecycle.OnStop(ctx, "late", func() error {
			<-released
			log.Info("after the harness returned")
			return nil
		})
		for i := range 8 {
			lifecycle.Go(ctx, "w", func(ctx context.Context) error {
				for range 50 {
					log.InfoContext(ctx, "tick", "worker", i)
				}
				return nil
			})
		}
		lifecycle.Stop(ctx)
		<-ctx.Done()
		return nil
	}, lifecycle.Grace(100*time.Millisecond))
	close(released)
	n := len(res.Lines)
	lines := decode(t, res)
	ticks := 0
	for _, l := range lines {
		if l.str("body") == "tick" {
			ticks++
		}
	}
	if ticks != 400 || res.Reason != lifecycle.ReasonForced {
		t.Errorf("%d tick lines, reason %q; want 400 whole lines and a forced stop", ticks, res.Reason)
	}
	time.Sleep(20 * time.Millisecond)
	if len(res.Lines) != n {
		t.Error("Lines changed after Run returned")
	}
}
