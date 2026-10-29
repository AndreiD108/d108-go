package lifecycle

import (
	"log/slog"
	"time"

	"github.com/AndreiD108/d108-go/lifecycle/internal/run"
	"github.com/AndreiD108/d108-go/logging"
)

// heartbeat writes process.heartbeat every interval until the stop sequence starts.
func heartbeat(r *run.Run, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-r.Stopping():
			return
		case <-t.C:
			r.Log().LogAttrs(r.Context(), slog.LevelInfo, "", logging.Event("process.heartbeat"))
		}
	}
}
