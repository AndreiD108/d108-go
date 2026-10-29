// Package failure classes errors and maps the classes to the shared exit-code table of
// standard.md section 9.2. A classed error carries a message, the next step the caller
// should take, and details for the error envelope the CLI writes on stderr.
package failure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Class is the stable failure category. Its string and its code are part of the output
// contract of every tool.
type Class string

// The shared classes. Every tool gives them the same meaning and the same exit code.
const (
	OK         Class = "ok"
	Internal   Class = "internal"
	Usage      Class = "usage"
	Auth       Class = "auth"
	Permission Class = "permission"
	NotFound   Class = "not_found"
	Conflict   Class = "conflict"
	Remote     Class = "remote"
	Timeout    Class = "timeout"
	Cancelled  Class = "cancelled"
	Terminated Class = "terminated"
)

var (
	mu    sync.RWMutex
	codes = map[Class]int{
		OK:         0,
		Internal:   1,
		Usage:      2,
		Auth:       3,
		Permission: 4,
		NotFound:   5,
		Conflict:   6,
		Remote:     7,
		Timeout:    8,
		Cancelled:  130,
		Terminated: 143,
	}
)

// Register adds an app class with its exit code. Codes 9 to 125 are the app's; the shared
// table owns 0 to 8, 130 and 143. It panics on a code outside that range or on a class or
// code already taken, because the tool's exit codes would then be ambiguous.
func Register(class Class, code int) {
	if code < 9 || code >= 126 {
		panic(fmt.Sprintf("failure.Register(%q, %d): app codes are 9 to 125", class, code))
	}
	mu.Lock()
	defer mu.Unlock()
	if _, taken := codes[class]; taken {
		panic(fmt.Sprintf("failure.Register(%q, %d): class already registered", class, code))
	}
	for c, existing := range codes {
		if existing == code {
			panic(fmt.Sprintf("failure.Register(%q, %d): code taken by %q", class, code, c))
		}
	}
	codes[class] = code
}

// Code returns the exit code of a class; an unknown class is Internal's.
func Code(class Class) int {
	mu.RLock()
	defer mu.RUnlock()
	if code, ok := codes[class]; ok {
		return code
	}
	return codes[Internal]
}

// Error is a classed error.
type Error struct {
	Class    Class
	Message  string
	NextStep string
	Details  map[string]any
	cause    error
}

// New makes a classed error. details are key, value pairs for the envelope. It panics on an
// empty nextStep: every failure tells the caller what to do next.
func New(class Class, message, nextStep string, details ...any) error {
	if nextStep == "" {
		panic(fmt.Sprintf("failure.New(%q, %q): empty nextStep", class, message))
	}
	return &Error{Class: class, Message: message, NextStep: nextStep, Details: detailMap(details)}
}

// Wrap is New with a cause kept for errors.Is and errors.As.
func Wrap(cause error, class Class, message, nextStep string, details ...any) error {
	e := New(class, message, nextStep, details...).(*Error)
	e.cause = cause
	return e
}

func detailMap(details []any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	m := make(map[string]any, len(details)/2)
	for i := 0; i < len(details); i += 2 {
		key := fmt.Sprint(details[i])
		if i+1 < len(details) {
			m[key] = details[i+1]
		} else {
			m[key] = nil
		}
	}
	return m
}

func (e *Error) Error() string { return string(e.Class) + ": " + e.Message }

// Unwrap returns the cause given to Wrap.
func (e *Error) Unwrap() error { return e.cause }

// ClassOf returns the class of err: OK for nil, the error's own class when it has one,
// Cancelled for context.Canceled, Timeout for context.DeadlineExceeded, Internal otherwise.
func ClassOf(err error) Class {
	return From(err).Class
}

// Is reports whether err has the class.
func Is(err error, class Class) bool { return ClassOf(err) == class }

// ExitCode returns the exit code for err; 0 for nil.
func ExitCode(err error) int { return Code(ClassOf(err)) }

// From turns any error into a classed Error by the rules of ClassOf. It returns a value with
// class OK for nil.
func From(err error) *Error {
	if err == nil {
		return &Error{Class: OK}
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Class: Cancelled, Message: "cancelled before completion", NextStep: "rerun the command if the result is still needed", cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Class: Timeout, Message: "deadline exceeded", NextStep: "rerun; if it repeats, raise the timeout or check the remote service", cause: err}
	}
	return &Error{
		Class:    Internal,
		Message:  "unexpected internal error",
		NextStep: "rerun once; if it repeats, report it with the command line and this error",
		Details:  map[string]any{"cause": err.Error()},
		cause:    err,
	}
}

type envelope struct {
	Class    Class          `json:"class"`
	Message  string         `json:"message"`
	NextStep string         `json:"next_step"`
	Details  map[string]any `json:"details,omitempty"`
}

// Write encodes err as the one-line JSON error envelope of standard.md section 9.2:
// {"class","message","next_step","details"}. Details that cannot be encoded are dropped;
// class, message and next step always survive.
func Write(w io.Writer, err error) error {
	e := From(err)
	env := envelope{Class: e.Class, Message: e.Message, NextStep: e.NextStep, Details: e.Details}
	line, encErr := encode(env)
	if encErr != nil {
		env.Details = nil
		if line, encErr = encode(env); encErr != nil {
			return encErr
		}
	}
	_, writeErr := w.Write(line)
	return writeErr
}

func encode(env envelope) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
