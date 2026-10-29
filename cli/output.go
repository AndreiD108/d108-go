package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/AndreiD108/d108-go/failure"
)

// Format is an output format a command declares and the user selects with --format.
type Format string

// The formats of standard.md section 9.2.
const (
	JSON  Format = "json"
	JSONL Format = "jsonl"
	Text  Format = "text"
)

// Output is what a runner writes its result through. Every write goes to stdout at once;
// nothing else writes there. The runner reads Format and uses the matching method.
type Output interface {
	// Format is the format the user selected, or the command's default.
	Format() Format
	// JSON writes v as one compact JSON object followed by a newline. A second call is an
	// internal failure: a json result is one object.
	JSON(v any) error
	// JSONL writes v as one JSON line.
	JSONL(v any) error
	// Text writes fmt.Sprintf(format, args...) as one write.
	Text(format string, args ...any) error
}

// output is the Output of one run.
type output struct {
	w      io.Writer
	format Format
	mu     sync.Mutex
	wrote  bool
}

func newOutput(w io.Writer, format Format) *output {
	return &output{w: w, format: format}
}

func (o *output) Format() Format { return o.format }

func (o *output) JSON(v any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.wrote {
		return failure.New(failure.Internal, "the command wrote a second JSON object",
			"report it: a json result is one object; stream with --format jsonl")
	}
	line, err := encodeLine(v)
	if err != nil {
		return err
	}
	o.wrote = true
	return o.write(line)
}

func (o *output) JSONL(v any) error {
	line, err := encodeLine(v)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.write(line)
}

func (o *output) Text(format string, args ...any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.write([]byte(fmt.Sprintf(format, args...)))
}

// write is one Write call; a failure is classed internal with the cause.
func (o *output) write(b []byte) error {
	if _, err := o.w.Write(b); err != nil {
		return failure.Wrap(err, failure.Internal, "writing the result to stdout failed: "+err.Error(),
			"check what reads the command's stdout")
	}
	return nil
}

// encodeLine encodes v as one JSON line without HTML escaping.
func encodeLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, failure.Wrap(err, failure.Internal, "the result cannot be encoded as JSON: "+err.Error(),
			"report it with the command line")
	}
	return buf.Bytes(), nil
}

// formatList renders declared formats for help: "json (default), jsonl, text".
func formatList(formats []Format) string {
	parts := make([]string, len(formats))
	for i, f := range formats {
		parts[i] = string(f)
		if i == 0 {
			parts[i] += " (default)"
		}
	}
	return strings.Join(parts, ", ")
}
