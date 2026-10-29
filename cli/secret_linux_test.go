package cli_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/AndreiD108/d108-go/cli"
	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
)

// pty opens a pseudo-terminal pair: the master as a file to type into, the slave as the
// blocking file a real stdin is, with its descriptor for the attribute checks.
func pty(t *testing.T) (master *os.File, slave *os.File, slaveFd int) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("ptsname: %v", err)
	}
	slaveFd, err = unix.Open("/dev/pts/"+strconv.Itoa(n), unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open slave: %v", err)
	}
	slave = os.NewFile(uintptr(slaveFd), "stdin")
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave, slaveFd
}

func echoOn(t *testing.T, fd int) bool {
	t.Helper()
	attrs, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatalf("tcgetattr: %v", err)
	}
	return attrs.Lflag&unix.ECHO != 0
}

func nonblocking(t *testing.T, fd int) bool {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("fcntl: %v", err)
	}
	return flags&unix.O_NONBLOCK != 0
}

// waitEchoOff polls until ReadSecret has turned echo off, which is when it is at the read.
func waitEchoOff(t *testing.T, fd int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for echoOn(t, fd) {
		if time.Now().After(deadline) {
			t.Fatal("echo stayed on: ReadSecret did not reach the read")
		}
		time.Sleep(time.Millisecond)
	}
}

type result struct {
	err            error
	stdout, stderr string
}

// runSecret runs `tool secret` with the slave as stdin in the background.
func runSecret(ctx context.Context, t *testing.T, slave *os.File) <-chan result {
	t.Helper()
	root := tree(t)
	log := logging.New(cli.IdentityOf(root), logging.Writer(io.Discard), logging.Level(slog.LevelDebug))
	done := make(chan result, 1)
	go func() {
		var out, errOut bytes.Buffer
		err := cli.Execute(logging.NewContext(ctx, log), root, []string{"secret"}, slave, &out, &errOut)
		done <- result{err, out.String(), errOut.String()}
	}()
	return done
}

func TestReadSecretOnATerminal(t *testing.T) {
	master, slave, fd := pty(t)
	if !echoOn(t, fd) {
		t.Fatal("a fresh pty has echo on")
	}
	done := runSecret(t.Context(), t, slave)
	waitEchoOff(t, fd)
	if !nonblocking(t, fd) {
		t.Error("stdin is not non-blocking during the read")
	}
	if _, err := master.WriteString("hunter2\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.stdout != `{"secret":"hunter2"}`+"\n" || r.stderr != "Key: \n" {
			t.Errorf("err %v, stdout %q, stderr %q", r.err, r.stdout, r.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadSecret did not return after the line")
	}
	if !echoOn(t, fd) {
		t.Error("echo is off after ReadSecret")
	}
	if nonblocking(t, fd) {
		t.Error("stdin is non-blocking after ReadSecret")
	}
}

func TestReadSecretCancelledRestoresTheTerminal(t *testing.T) {
	_, slave, fd := pty(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := runSecret(ctx, t, slave)
	waitEchoOff(t, fd)
	cancel()
	select {
	case r := <-done:
		if !failure.Is(r.err, failure.Cancelled) || r.stdout != "" || !strings.HasPrefix(r.stderr, "Key: \n") {
			t.Errorf("err %v, stdout %q, stderr %q", r.err, r.stdout, r.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadSecret did not return on cancel")
	}
	if !echoOn(t, fd) {
		t.Error("echo is off after the cancel")
	}
	if nonblocking(t, fd) {
		t.Error("stdin is non-blocking after the cancel")
	}
}
