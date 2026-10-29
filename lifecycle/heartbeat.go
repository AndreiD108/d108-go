package lifecycle

import (
	"log/slog"
	"time"

	"github.com/AndreiD108/d108-go/logging"
)

// heartbeat writes process.heartbeat every interval until the stop sequence starts.
func (r *run) heartbeat(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-r.stopping:
			return
		case <-t.C:
			r.log.LogAttrs(r.ctx, slog.LevelInfo, "", logging.Event("process.heartbeat"))
		}
	}
}
