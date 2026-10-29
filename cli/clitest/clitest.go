// Package clitest runs a tree made with cli in a test: one call gives the exit code, stdout
// and stderr of a command, and Lines gives the log lines it wrote.
//
//	exit, stdout, stderr := clitest.Run(t, root, "", "messages", "search", "--format", "jsonl", "deploy")
//
// The runner gets the test's ctx, not one from lifecycle.Run: a runner that calls
// lifecycle.OnStop, Stop or Go panics here (an internal failure in the result). Commands
// rarely need them (standard.md section 9.1); one that does is tested as lifecycle tests
// itself, in a child process.
package clitest

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/AndreiD108/d108-go/cli"
	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
)

var (
	mu      sync.Mutex
	buffers = map[testing.TB]*lockedBuffer{}
)

// lockedBuffer takes the lines of a logger used from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// Run runs the tree as cli.Execute does, with stdin as the input, and returns the exit code
// lifecycle.Run would use with stdout and stderr. The log lines go to a buffer Lines reads,
// at level debug. root must come from cli.Root.
func Run(t testing.TB, root *cobra.Command, stdin string, args ...string) (exit int, stdout, stderr string) {
	return RunContext(t.Context(), t, root, stdin, args...)
}

// RunContext is Run with the caller's ctx, for a test that cancels the command.
func RunContext(ctx context.Context, t testing.TB, root *cobra.Command, stdin string, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	log := logging.New(cli.IdentityOf(root), logging.Writer(buffer(t)), logging.Level(slog.LevelDebug))
	var out, errOut bytes.Buffer
	err := cli.Execute(logging.NewContext(ctx, log), root, args, strings.NewReader(stdin), &out, &errOut)
	return failure.ExitCode(err), out.String(), errOut.String()
}

// Raw returns every log line the test's runs wrote so far, as written, each with its
// newline; for a check against the contract with logcheck.
func Raw(t testing.TB) [][]byte {
	var lines [][]byte
	for _, raw := range bytes.Split(buffer(t).bytes(), []byte("\n")) {
		if len(raw) > 0 {
			lines = append(lines, append(raw, '\n'))
		}
	}
	return lines
}

// Lines returns every log line the test's runs wrote so far, each decoded as a JSON object,
// in order.
func Lines(t testing.TB) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range Raw(t) {
		var line map[string]any
		if err := json.Unmarshal(raw, &line); err != nil {
			t.Fatalf("clitest: log line is not JSON: %v: %s", err, raw)
		}
		lines = append(lines, line)
	}
	return lines
}

// buffer returns the test's log buffer, made on first use and dropped when the test ends.
func buffer(t testing.TB) *lockedBuffer {
	mu.Lock()
	defer mu.Unlock()
	if b, ok := buffers[t]; ok {
		return b
	}
	b := &lockedBuffer{}
	buffers[t] = b
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		delete(buffers, t)
	})
	return b
}
