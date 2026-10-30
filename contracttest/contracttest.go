// Package contracttest checks log lines against the d108 contract in a test. It runs the
// logcheck binary of the private contract repo, found on PATH, and skips the test when the
// binary is not there, so a clone without it still passes go test:
//
//	t.Run("contract", func(t *testing.T) { contracttest.Check(t, clitest.Raw(t)) })
//
// Put the call in its own subtest when other assertions follow it: a skip ends the test it
// is called in.
package contracttest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Install is the command that puts logcheck on PATH.
const Install = "GOPRIVATE=github.com/AndreiD108/* go install github.com/AndreiD108/d108/logcheck/cmd/logcheck@v0.1.0"

// Check runs logcheck once over the lines, each ending in a newline, and fails t with its
// report when logcheck does not exit 0. Line numbers in the report count from 1 in the order
// of lines. Only a logcheck missing from PATH skips t.
func Check(t testing.TB, lines [][]byte) {
	t.Helper()
	bin, err := exec.LookPath("logcheck")
	if err != nil {
		t.Skipf("logcheck is not on PATH (%v); install it: %s", err, Install)
	}
	for i, l := range lines {
		if len(l) == 0 || l[len(l)-1] != '\n' {
			t.Fatalf("line %d does not end in a newline: %.80q", i+1, l)
		}
	}
	path := filepath.Join(t.TempDir(), "lines.jsonl")
	if err := os.WriteFile(path, bytes.Join(lines, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, path).CombinedOutput(); err != nil {
		t.Errorf("logcheck over %d lines: %v\n%s", len(lines), err, out)
	}
}
