package failure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSharedTable(t *testing.T) {
	want := map[Class]int{
		OK: 0, Internal: 1, Usage: 2, Auth: 3, Permission: 4, NotFound: 5, Conflict: 6,
		Remote: 7, Timeout: 8, Cancelled: 130, Terminated: 143,
	}
	for class, code := range want {
		if got := Code(class); got != code {
			t.Errorf("Code(%q) = %d, want %d", class, got, code)
		}
	}
	if got := Code("unknown"); got != 1 {
		t.Errorf("Code(unknown) = %d, want 1", got)
	}
}

func TestClassOf(t *testing.T) {
	cases := []struct {
		err  error
		want Class
		code int
	}{
		{nil, OK, 0},
		{New(NotFound, "no such channel", "list channels"), NotFound, 5},
		{fmt.Errorf("wrapped: %w", New(Auth, "token rejected", "run login")), Auth, 3},
		{context.Canceled, Cancelled, 130},
		{fmt.Errorf("op: %w", context.DeadlineExceeded), Timeout, 8},
		{errors.New("plain"), Internal, 1},
	}
	for _, c := range cases {
		if got := ClassOf(c.err); got != c.want {
			t.Errorf("ClassOf(%v) = %q, want %q", c.err, got, c.want)
		}
		if got := ExitCode(c.err); got != c.code {
			t.Errorf("ExitCode(%v) = %d, want %d", c.err, got, c.code)
		}
		if !Is(c.err, c.want) {
			t.Errorf("Is(%v, %q) is false", c.err, c.want)
		}
	}
}

func TestWrapKeepsCause(t *testing.T) {
	cause := errors.New("dial tcp: i/o timeout")
	err := Wrap(cause, Remote, "slack did not answer", "retry in a minute", "attempt", 3)
	if !errors.Is(err, cause) {
		t.Error("errors.Is(err, cause) is false")
	}
	if err.Error() != "remote: slack did not answer" {
		t.Errorf("Error() = %q", err.Error())
	}
	if From(err).Details["attempt"] != 3 {
		t.Errorf("Details = %v", From(err).Details)
	}
}

func TestNewPanicsOnEmptyNextStep(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New with an empty nextStep did not panic")
		}
	}()
	New(Usage, "bad flag", "")
}

func TestRegister(t *testing.T) {
	Register("quota", 9)
	if got := Code("quota"); got != 9 {
		t.Errorf("Code(quota) = %d", got)
	}
	for _, c := range []struct {
		class Class
		code  int
	}{{"low", 8}, {"high", 126}, {"quota", 10}, {"again", 9}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%q, %d) did not panic", c.class, c.code)
				}
			}()
			Register(c.class, c.code)
		}()
	}
}

func TestWriteEnvelope(t *testing.T) {
	var buf bytes.Buffer
	err := New(Usage, "unknown format", "use one of json, text", "format", "xml")
	if werr := Write(&buf, err); werr != nil {
		t.Fatal(werr)
	}
	line := buf.String()
	if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") {
		t.Errorf("envelope is not one line: %q", line)
	}
	var got map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &got); jerr != nil {
		t.Fatal(jerr)
	}
	if got["class"] != "usage" || got["message"] != "unknown format" || got["next_step"] != "use one of json, text" {
		t.Errorf("envelope = %v", got)
	}
	if got["details"].(map[string]any)["format"] != "xml" {
		t.Errorf("details = %v", got["details"])
	}

	buf.Reset()
	if werr := Write(&buf, New(Internal, "x", "y", "bad", func() {})); werr != nil {
		t.Fatal(werr)
	}
	if strings.Contains(buf.String(), "details") {
		t.Errorf("unencodable details were not dropped: %s", buf.String())
	}

	buf.Reset()
	if werr := Write(&buf, errors.New("boom")); werr != nil {
		t.Fatal(werr)
	}
	if !strings.Contains(buf.String(), `"class":"internal"`) || !strings.Contains(buf.String(), `"cause":"boom"`) {
		t.Errorf("plain error envelope = %s", buf.String())
	}
}
