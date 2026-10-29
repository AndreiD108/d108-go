package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/lifecycle"
	"github.com/AndreiD108/d108-go/logging"
)

// The child process reads these; TestMain runs the scenario instead of the tests.
const (
	envScenario = "D108_LIFECYCLE_SCENARIO"
	envLogDir   = "D108_LIFECYCLE_LOGDIR"
	envSignal   = "D108_LIFECYCLE_SIGNAL"
)

type scenario struct {
	fn   lifecycle.Func
	opts []lifecycle.Option
}

// logStep registers a stop step that writes one line, so the test reads the order steps ran in.
func logStep(ctx context.Context, log *slog.Logger, name string, err error) {
	lifecycle.OnStop(ctx, name, func() error {
		log.InfoContext(ctx, "step ran", "step", name)
		return err
	})
}

func selfSignal(sig syscall.Signal) {
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		panic(err)
	}
}

func signalFromEnv() syscall.Signal {
	switch os.Getenv(envSignal) {
	case "SIGHUP":
		return syscall.SIGHUP
	case "SIGINT":
		return syscall.SIGINT
	default:
		return syscall.SIGTERM
	}
}

var scenarios = map[string]scenario{
	"clean": {fn: func(ctx context.Context, log *slog.Logger) error {
		logStep(ctx, log, "a", nil)
		logStep(ctx, log, "b", nil)
		logStep(ctx, log, "c", nil)
		return nil
	}},
	"signal": {fn: func(ctx context.Context, log *slog.Logger) error {
		logStep(ctx, log, "a", nil)
		selfSignal(signalFromEnv())
		<-ctx.Done()
		return ctx.Err()
	}},
	"stop": {fn: func(ctx context.Context, log *slog.Logger) error {
		logStep(ctx, log, "db", nil)
		lifecycle.Go(ctx, "worker", func(ctx context.Context) error {
			lifecycle.Stop(ctx)
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond)
			log.InfoContext(ctx, "worker done")
			return ctx.Err()
		})
		<-ctx.Done()
		return ctx.Err()
	}},
	"failstep": {fn: func(ctx context.Context, log *slog.Logger) error {
		logStep(ctx, log, "a", nil)
		logStep(ctx, log, "b", errors.New("close failed"))
		lifecycle.OnStop(ctx, "c", func() error { panic("step boom") })
		return failure.New(failure.NotFound, "no index", "run index rebuild")
	}},
	"hang": {fn: func(ctx context.Context, log *slog.Logger) error {
		logStep(ctx, log, "a", nil)
		lifecycle.OnStop(ctx, "slow", func() error { select {} })
		return nil
	}, opts: []lifecycle.Option{lifecycle.Grace(200 * time.Millisecond)}},
	"gopanic": {fn: func(ctx context.Context, log *slog.Logger) error {
		logStep(ctx, log, "a", nil)
		lifecycle.Go(ctx, "worker", func(ctx context.Context) error { panic("boom") })
		<-ctx.Done()
		return ctx.Err()
	}},
	"fnpanic": {fn: func(ctx context.Context, log *slog.Logger) error {
		logStep(ctx, log, "a", nil)
		panic(fmt.Errorf("fn boom"))
	}},
	"goerror": {fn: func(ctx context.Context, log *slog.Logger) error {
		lifecycle.Go(ctx, "worker", func(ctx context.Context) error {
			return failure.New(failure.Remote, "upstream gone", "retry later")
		})
		<-ctx.Done()
		return ctx.Err()
	}},
	"doublesigint": {fn: func(ctx context.Context, log *slog.Logger) error {
		lifecycle.OnStop(ctx, "slow", func() error {
			go func() {
				time.Sleep(100 * time.Millisecond)
				selfSignal(syscall.SIGINT)
			}()
			select {}
		})
		selfSignal(syscall.SIGINT)
		<-ctx.Done()
		return ctx.Err()
	}},
	"cap": {fn: func(ctx context.Context, log *slog.Logger) error {
		filler := strings.Repeat("x", 200)
		for i := 0; i < 200; i++ {
			log.InfoContext(ctx, "filler", "i", i, "data", filler)
		}
		return nil
	}, opts: []lifecycle.Option{lifecycle.Logging(logging.MaxFileBytes(16 << 10))}},
	"lateerror": {fn: func(ctx context.Context, log *slog.Logger) error {
		lifecycle.Go(ctx, "worker", func(ctx context.Context) error {
			lifecycle.Stop(ctx)
			return nil
		})
		<-ctx.Done()
		return errors.New("listener broke")
	}},
	"latepanic": {fn: func(ctx context.Context, log *slog.Logger) error {
		lifecycle.Stop(ctx)
		<-ctx.Done()
		panic("after stop")
	}},
	"lategoroutine": {fn: func(ctx context.Context, log *slog.Logger) error {
		lifecycle.Stop(ctx)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		lifecycle.Go(ctx, "late", func(ctx context.Context) error { log.InfoContext(ctx, "late ran"); return nil })
		return nil
	}},
	"hangafterfail": {fn: func(ctx context.Context, log *slog.Logger) error {
		lifecycle.OnStop(ctx, "slow", func() error { select {} })
		logStep(ctx, log, "b", errors.New("close failed"))
		return nil
	}, opts: []lifecycle.Option{lifecycle.Grace(200 * time.Millisecond)}},
	"termthenint": {fn: func(ctx context.Context, log *slog.Logger) error {
		lifecycle.OnStop(ctx, "slow", func() error {
			go func() {
				time.Sleep(100 * time.Millisecond)
				selfSignal(syscall.SIGINT)
			}()
			select {}
		})
		selfSignal(syscall.SIGTERM)
		<-ctx.Done()
		return ctx.Err()
	}},
	"flush": {fn: func(ctx context.Context, log *slog.Logger) error {
		return nil
	}, opts: []lifecycle.Option{lifecycle.Grace(300 * time.Millisecond)}},
	"forcedflush": {fn: func(ctx context.Context, log *slog.Logger) error {
		lifecycle.OnStop(ctx, "slow", func() error { select {} })
		go func() {
			time.Sleep(600 * time.Millisecond)
			selfSignal(syscall.SIGINT)
		}()
		return nil
	}, opts: []lifecycle.Option{lifecycle.Grace(200 * time.Millisecond)}},
	"late-onstop": {fn: func(ctx context.Context, log *slog.Logger) error {
		lifecycle.Stop(ctx)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		lifecycle.OnStop(ctx, "late", func() error { log.InfoContext(ctx, "step ran", "step", "late"); return nil })
		return nil
	}},
}

// runScenario is the child's main: it never returns.
func runScenario(name string) {
	s, ok := scenarios[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown scenario %q\n", name)
		os.Exit(99)
	}
	opts := append([]lifecycle.Option{lifecycle.Logging(logging.Dir(os.Getenv(envLogDir)))}, s.opts...)
	lifecycle.Run(identity.Identity{Name: "lifecycle-test", Version: "1.0.0", InstanceID: "01JBX7Q2M9K4T8V6W3R5N1P0ZD", PID: os.Getpid()}, s.fn, opts...)
}
