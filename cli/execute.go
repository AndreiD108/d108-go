package cli

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
	"github.com/AndreiD108/d108-go/work"
)

// execution is the state of one Execute call, carried in ctx to the leaf's RunE.
type execution struct {
	log    *slog.Logger
	stdin  io.Reader
	stdout io.Writer
	// hooksDone is set when the command's hooks have run; ran when its runner was reached.
	// classify reads them to tell a hook error from Cobra's own checks.
	hooksDone, ran bool
}

type executionKey struct{}

func executionOf(ctx context.Context) *execution {
	ex, _ := ctx.Value(executionKey{}).(*execution)
	return ex
}

// Stdin returns the input stream Execute was given, for a runner that reads its input; the
// ctx is the runner's. Outside Execute it is os.Stdin.
func Stdin(ctx context.Context) io.Reader {
	if ex := executionOf(ctx); ex != nil {
		return ex.stdin
	}
	return os.Stdin
}

// Execute runs the tree once with the given streams and arguments (os.Args[1:] in main). The
// result goes to stdout; help and the error envelope go to stderr. It returns nil on success,
// else the classed error after writing the envelope: main returns it from the function
// lifecycle.Run runs, and Run exits with failure.ExitCode of it. The logger comes from ctx
// (logging.FromContext); Run puts it there. A cancelled ctx ends the command with the class
// cancelled (exit 130) once the runner returns ctx.Err(). A Cobra error before any runner
// ran (unknown command or flag, wrong argument count) is a usage error with Cobra's message.
// A tree runs once, because Cobra keeps the flag values and the leaf contexts of a run in
// the tree: a second Execute on the same root panics; tests build a tree per run.
func Execute(ctx context.Context, root *cobra.Command, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if root.Annotations[annExecuted] != "" {
		panic("cli.Execute: this tree already ran; build a new tree for each run")
	}
	root.Annotations[annExecuted] = "true"
	wrapValidation(root)
	ex := &execution{log: logging.FromContext(ctx), stdin: stdin, stdout: stdout}
	root.SetIn(stdin)
	root.SetOut(stderr)
	root.SetErr(stderr)
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(context.WithValue(ctx, executionKey{}, ex))
	if err != nil && !ex.ran {
		err = classify(cmd, ex, err)
		if cmd != nil && cmd != root {
			_, end := work.Command(ctx, ex.log, pathOf(cmd), flagsOf(cmd))
			end(err)
		}
	}
	if err != nil {
		_ = failure.Write(stderr, err)
	}
	return err
}
