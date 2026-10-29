package lifecycle

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/lifecycle/internal/run"
)

// codeHangup is the exit code of SIGHUP (128 + 1); the failure table has no class for it.
const codeHangup = 129

// notifySignals subscribes to SIGINT, SIGTERM and SIGHUP before fn starts, so a signal that
// arrives early is not lost.
func notifySignals() <-chan os.Signal {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return ch
}

// watchSignals turns the first signal into the stop cause. A signal after the sequence
// started forces the exit.
func watchSignals(r *run.Run, ch <-chan os.Signal) {
	for sig := range ch {
		code, name := signalCode(sig)
		select {
		case <-r.Stopping():
			r.Force(code)
		default:
			r.Stop(run.Cause{Reason: ReasonSignal, Code: code, Signal: name})
		}
	}
}

func signalCode(sig os.Signal) (int, string) {
	switch sig {
	case syscall.SIGHUP:
		return codeHangup, "SIGHUP"
	case syscall.SIGTERM:
		return failure.Code(failure.Terminated), "SIGTERM"
	default:
		return failure.Code(failure.Cancelled), "SIGINT"
	}
}
