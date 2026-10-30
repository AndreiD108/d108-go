package contracttest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type recorder struct {
	testing.TB
	skipped, failed, fatal bool
	msg                    string
}

func (r *recorder) Helper() {}

func (r *recorder) Skipf(format string, args ...any) {
	r.skipped, r.msg = true, fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (r *recorder) Errorf(format string, args ...any) {
	r.failed, r.msg = true, fmt.Sprintf(format, args...)
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.fatal, r.msg = true, fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (r *recorder) Fatal(args ...any) {
	r.fatal, r.msg = true, fmt.Sprint(args...)
	runtime.Goexit()
}

// check runs Check with a logcheck that is the given shell script, or none when script is "".
func check(t *testing.T, script string, lines [][]byte) *recorder {
	dir := t.TempDir()
	if script != "" {
		if err := os.WriteFile(filepath.Join(dir, "logcheck"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		Check(r, lines)
	}()
	<-done
	return r
}

var good = [][]byte{[]byte("{}\n")}

func TestOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, script           string
		lines                  [][]byte
		skipped, failed, fatal bool
	}{
		{name: "pass", script: "exit 0", lines: good},
		{name: "missing", script: "", lines: good, skipped: true},
		{name: "line fails", script: "echo '1: bad'; exit 1", lines: good, failed: true},
		{name: "usage", script: "exit 2", lines: good, failed: true},
		{name: "signal", script: "kill -9 $$", lines: good, failed: true},
		{name: "no newline", script: "exit 0", lines: [][]byte{[]byte("{}")}, fatal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := check(t, tc.script, tc.lines)
			if r.skipped != tc.skipped || r.failed != tc.failed || r.fatal != tc.fatal {
				t.Errorf("skipped %v failed %v fatal %v (%s); want %v %v %v", r.skipped, r.failed, r.fatal, r.msg, tc.skipped, tc.failed, tc.fatal)
			}
		})
	}
}

func TestReportReachesTheFailure(t *testing.T) {
	r := check(t, "echo '2: schema: missing body'; exit 1", good)
	if !r.failed || !strings.Contains(r.msg, "2: schema: missing body") {
		t.Errorf("message %q", r.msg)
	}
}

func TestOneRunOverTheJoinedLines(t *testing.T) {
	out := filepath.Join(t.TempDir(), "seen")
	r := check(t, `/bin/cat "$1" > `+out, [][]byte{[]byte("a\n"), []byte("b\n")})
	if r.failed || r.skipped || r.fatal {
		t.Fatalf("%+v", r)
	}
	seen, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(seen) != "a\nb\n" {
		t.Errorf("logcheck saw %q", seen)
	}
}
