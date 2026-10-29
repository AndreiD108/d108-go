package lifecycle

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/AndreiD108/d108-go/failure"
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
func (r *run) watchSignals(ch <-chan os.Signal) {
	for sig := range ch {
		code, name := signalCode(sig)
		select {
		case <-r.stopping:
			r.force(code)
		default:
			r.stop(cause{reason: ReasonSignal, code: code, signal: name})
		}
	}
}

// force ends the sequence at once; code is the forcing signal's exit code, the process exits
// with it. A second forcing signal cuts the flush that follows the forced process.stop.
func (r *run) force(code int) {
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
