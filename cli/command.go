package cli

import (
	"context"
	"sort"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/work"
)

// Runner is a leaf's function: args are the positional arguments after Cobra parsed the
// flags; out writes the result in the selected format. The error it returns is classed by
// failure and becomes the envelope and the exit code.
type Runner func(ctx context.Context, args []string, out Output) error

// CommandOption configures Command.
type CommandOption func(*commandOptions)

type commandOptions struct {
	formats []Format
}

// Formats declares the formats a command accepts; the first is its default. Without it a
// command accepts json only. It panics on an empty list or a repeated format.
func Formats(formats ...Format) CommandOption {
	if len(formats) == 0 {
		panic("cli.Formats: at least one format")
	}
	seen := map[Format]bool{}
	for _, f := range formats {
		if seen[f] {
			panic("cli.Formats: " + string(f) + " repeated")
		}
		seen[f] = true
	}
	return func(o *commandOptions) { o.formats = formats }
}

// formatsAnnotation is the Annotations key the usage template reads.
const formatsAnnotation = "cli.formats"

// Command makes a leaf under parent and returns it; flags, Long, Example and Args are set on
// the returned command. Execute runs run inside work.Command, with the selected format
// checked against the declaration.
func Command(parent *cobra.Command, name, short string, run Runner, opts ...CommandOption) *cobra.Command {
	o := commandOptions{formats: []Format{JSON}}
	for _, opt := range opts {
		opt(&o)
	}
	c := &cobra.Command{
		Use:         name,
		Short:       short,
		Annotations: map[string]string{formatsAnnotation: formatList(o.formats)},
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runLeaf(cmd, args, run, o.formats)
	}
	parent.AddCommand(c)
	return c
}

// runLeaf is the RunE of every leaf: it marks the run as started, selects the format, opens
// the unit of work, runs the runner with a recover, and ends the unit with the result.
func runLeaf(cmd *cobra.Command, args []string, run Runner, formats []Format) error {
	ex := executionOf(cmd.Context())
	if ex == nil {
		panic("cli: the tree must run through cli.Execute or clitest.Run")
	}
	ex.ran = true
	format, err := selectFormat(cmd, formats)
	ctx, end := work.Command(cmd.Context(), ex.log, pathOf(cmd), flagsOf(cmd))
	if err == nil {
		err = call(ctx, run, args, newOutput(ex.stdout, format))
	}
	end(err)
	return err
}

// call runs the runner; a panic comes back as a work.PanicError, which is an internal failure.
func call(ctx context.Context, run Runner, args []string, out Output) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = work.NewPanicError(v)
		}
	}()
	return run(ctx, args, out)
}

// selectFormat reads --format and checks it against the declaration; a flag not given means
// the first. A given empty value is refused like any other undeclared one.
func selectFormat(cmd *cobra.Command, formats []Format) (Format, error) {
	flag := cmd.Flags().Lookup(FormatFlag)
	if flag == nil || !flag.Changed {
		return formats[0], nil
	}
	value := flag.Value.String()
	for _, f := range formats {
		if Format(value) == f {
			return f, nil
		}
	}
	names := make([]string, len(formats))
	for i, f := range formats {
		names[i] = string(f)
	}
	return "", failure.New(failure.Usage, "--format "+strconv.Quote(value)+" is not a format of `"+cmd.CommandPath()+"`",
		"use --format with one of: "+formatList(formats), "command", pathOf(cmd), "formats", names)
}

// flagsOf lists the flags the user set, "--name" each, sorted, for args.flags.
func flagsOf(cmd *cobra.Command) []string {
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			names = append(names, "--"+f.Name)
		}
	})
	sort.Strings(names)
	return names
}
