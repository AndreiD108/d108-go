package lifecycle

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/lifecycle/internal/run"
	"github.com/AndreiD108/d108-go/logging"
)

func TestHeartbeatUntilStop(t *testing.T) {
	var buf bytes.Buffer
	id := identity.Identity{Name: "beat", Version: "1.0.0", InstanceID: "01JBX7Q2M9K4T8V6W3R5N1P0ZD", PID: os.Getpid()}
	r := run.New(logging.New(id, logging.Writer(&buf)), 0, nil)
	done := make(chan struct{})
	go func() { heartbeat(r, 10*time.Millisecond); close(done) }()
	time.Sleep(55 * time.Millisecond)
	r.Stop(run.Cause{Reason: ReasonApp})
	<-done
	n := strings.Count(buf.String(), `"event_name":"process.heartbeat"`)
	if n < 2 || n > 6 {
		t.Errorf("%d heartbeats in 55 ms at 10 ms, want 2 to 6", n)
	}
}

func TestSecondRunPanics(t *testing.T) {
	started.Store(true)
	t.Cleanup(func() { started.Store(false) })
	defer func() {
		if recover() == nil {
			t.Error("a second Run must panic")
		}
	}()
	id := identity.Identity{Name: "twice", Version: "1.0.0", InstanceID: "01JBX7Q2M9K4T8V6W3R5N1P0ZD", PID: os.Getpid()}
	start(id, nil)
}

func TestForeignContextPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("OnStop with a ctx not from Run must panic")
		}
	}()
	OnStop(t.Context(), "x", func() error { return nil })
}
