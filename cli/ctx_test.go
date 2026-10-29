package cli_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AndreiD108/d108-go/cli"
	"github.com/AndreiD108/d108-go/cli/clitest"
	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
)

func TestRunnerSeesItsCommandThroughCtx(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "where", "x", "--limit", "3")
	if exit != 0 || stdout != `{"changed":true,"limit":3,"path":"tool where"}`+"\n" || stderr != "note\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	if line := commandRun(t); line["command"] != "where" {
		t.Errorf("command.run %v", line)
	}
	exit, stdout, _ = clitest.Run(t, tree(t), "", "where")
	if exit != 0 || stdout != `{"changed":false,"limit":20,"path":"tool where"}`+"\n" {
		t.Errorf("exit %d, stdout %q", exit, stdout)
	}
}

func TestUseLineNamesTheCommandByItsFirstWord(t *testing.T) {
	exit, _, stderr := clitest.Run(t, tree(t), "", "where", "--help")
	if exit != 0 || !strings.Contains(stderr, "tool where [thing]") {
		t.Errorf("exit %d, help %q", exit, stderr)
	}
	_, _, stderr = clitest.Run(t, tree(t), "", "--help")
	if !strings.Contains(stderr, "\n  where ") {
		t.Errorf("root help lists where: %q", stderr)
	}
}

func TestAccessorsOutsideARunner(t *testing.T) {
	ctx := context.Background()
	if cli.Flags(ctx) != nil || cli.CommandPath(ctx) != "" || cli.Stderr(ctx) != os.Stderr || cli.Stdin(ctx) != os.Stdin {
		t.Error("the accessors outside a runner")
	}
}

func TestReadSecretPiped(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "hunter2\nsecond line\n", "secret")
	if exit != 0 || stdout != `{"secret":"hunter2"}`+"\n" || stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	exit, stdout, _ = clitest.Run(t, tree(t), "no newline", "secret")
	if exit != 0 || stdout != `{"secret":"no newline"}`+"\n" {
		t.Errorf("exit %d, stdout %q", exit, stdout)
	}
	exit, stdout, stderr = clitest.Run(t, tree(t), "", "secret")
	if exit != 2 || stdout != "" || envelope(t, stderr)["class"] != "usage" {
		t.Errorf("empty stdin: exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
}

// runSecretWith runs `tool secret` with stdin as the input in the background.
func runSecretWith(ctx context.Context, t *testing.T, stdin io.Reader) <-chan error {
	t.Helper()
	root := tree(t)
	log := logging.New(cli.IdentityOf(root), logging.Writer(io.Discard), logging.Level(slog.LevelDebug))
	done := make(chan error, 1)
	go func() {
		var out, errOut bytes.Buffer
		done <- cli.Execute(logging.NewContext(ctx, log), root, []string{"secret"}, stdin, &out, &errOut)
	}()
	return done
}

func TestReadSecretCancelEndsABlockedReader(t *testing.T) {
	for name, open := range map[string]func(t *testing.T) io.Reader{
		"io.Pipe": func(t *testing.T) io.Reader {
			r, w := io.Pipe()
			t.Cleanup(func() { _ = w.Close() })
			return r
		},
		"os.Pipe": func(t *testing.T) io.Reader {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = w.Close(); _ = r.Close() })
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			done := runSecretWith(ctx, t, open(t))
			time.Sleep(20 * time.Millisecond)
			cancel()
			select {
			case err := <-done:
				if !failure.Is(err, failure.Cancelled) {
					t.Errorf("err %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("ReadSecret did not return on cancel")
			}
		})
	}
}

func TestReadSecretOnACancelledCtxLeavesTheInput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in := strings.NewReader("hunter2\n")
	if err := <-runSecretWith(ctx, t, in); !failure.Is(err, failure.Cancelled) || in.Len() != len("hunter2\n") {
		t.Errorf("err %v, %d bytes left", err, in.Len())
	}
}

func TestReadSecretFromAPipeFile(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if _, err := w.WriteString("hunter2\nrest\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	root := tree(t)
	log := logging.New(cli.IdentityOf(root), logging.Writer(io.Discard), logging.Level(slog.LevelDebug))
	var out, errOut bytes.Buffer
	if err := cli.Execute(logging.NewContext(t.Context(), log), root, []string{"secret"}, r, &out, &errOut); err != nil || out.String() != `{"secret":"hunter2"}`+"\n" || errOut.Len() != 0 {
		t.Errorf("err %v, stdout %q, stderr %q", err, out.String(), errOut.String())
	}
	rest, _ := io.ReadAll(r)
	if string(rest) != "rest\n" {
		t.Errorf("read past the line: %q left", rest)
	}
}
