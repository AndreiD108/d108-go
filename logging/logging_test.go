package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
)

var testID = identity.Identity{Name: "golden", Version: "1.0.0", InstanceID: "01JBX7Q2M9K4T8V6W3R5N1P0ZD", PID: 4242}

var fixedNow = time.Date(2026, 10, 27, 14, 3, 11, 482913000, time.UTC)

func clock(t time.Time) func() time.Time { return func() time.Time { return t } }

// capture returns a logger writing JSON lines to the returned buffer.
func capture(t *testing.T, opts ...Option) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GOLDEN_LOG_LEVEL", "")
	var buf bytes.Buffer
	opts = append([]Option{Writer(&buf), Now(clock(fixedNow))}, opts...)
	return New(testID, opts...), &buf
}

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSuffix(buf.Bytes(), []byte("\n")), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
		out = append(out, m)
	}
	return out
}

func one(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	ls := lines(t, buf)
	if len(ls) != 1 {
		t.Fatalf("want one line, got %d: %s", len(ls), buf.String())
	}
	return ls[0]
}

type stackErr struct{ msg string }

func (e stackErr) Error() string      { return e.msg }
func (e stackErr) StackTrace() string { return "main.go:1\nmain.go:2" }

func TestValueConversions(t *testing.T) {
	log, buf := capture(t)
	type point struct {
		X int      `json:"x"`
		Y []string `json:"y"`
	}
	log.Info("v",
		"dur", 1500*time.Millisecond,
		"when", time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600)),
		"nan", math.NaN(),
		"inf", math.Inf(-1),
		"pt", point{X: 1, Y: []string{"a"}},
		"m", map[string]any{"b": 2, "a": nil},
		"bytes", []byte("raw"),
		"u", uint64(7),
		"f", 2.5,
		slog.Group("empty"),
		slog.Group("g", slog.Int("n", 1), slog.Group("h", slog.Bool("ok", true))),
		"event_name", "not.an.event",
		strings.Repeat("k", 129), "dropped",
	)
	got := one(t, buf)
	want := map[string]any{
		"dur": "1.5s", "when": "2026-01-02T02:04:05Z", "nan": "NaN", "inf": "-Inf",
		"pt.x": json.Number("1"), "pt.y": `["a"]`, "m.b": json.Number("2"), "bytes": "raw",
		"u": json.Number("7"), "f": json.Number("2.5"), "g.n": json.Number("1"), "g.h.ok": true,
		"attr.event_name": "not.an.event",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
	for _, k := range []string{"m.a", "empty", strings.Repeat("k", 129), "event_name"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must not be written", k)
		}
	}
}

func TestErrorWithStack(t *testing.T) {
	log, buf := capture(t)
	log.Error("x", "err", stackErr{"boom"})
	got := one(t, buf)
	if got["exception.type"] != "logging.stackErr" || got["exception.message"] != "boom" || got["exception.stacktrace"] != "main.go:1\nmain.go:2" {
		t.Errorf("got %v", got)
	}
	if _, ok := got["err"]; ok {
		t.Error("the attribute key of an error must not be written")
	}
}

func TestFatalLevel(t *testing.T) {
	log, buf := capture(t)
	log.Log(context.Background(), LevelFatal, "down")
	got := one(t, buf)
	if got["severity_number"] != json.Number("21") || got["severity_text"] != "FATAL" {
		t.Errorf("got %v", got)
	}
}

func TestLoggerAttrsAndGroups(t *testing.T) {
	log, buf := capture(t)
	log.With("svc", "a").WithGroup("db").With("host", "h").Info("q", "ms", 3, logging_eventName())
	got := one(t, buf)
	if got["svc"] != "a" || got["db.host"] != "h" || got["db.ms"] != json.Number("3") || got["event_name"] != "db.query" {
		t.Errorf("got %v", got)
	}
}

func logging_eventName() slog.Attr { return Event("db.query") }

func TestContextAttrsPrecedence(t *testing.T) {
	log, buf := capture(t)
	ctx := WithAttrs(context.Background(), slog.String("caller.id", "s7"), slog.Int("x", 1))
	ctx = WithAttrs(ctx, slog.Int("y", 1))
	log.With("x", 0, "z", 0).InfoContext(ctx, "m", "x", 2)
	got := one(t, buf)
	if got["caller.id"] != "s7" || got["x"] != json.Number("2") || got["y"] != json.Number("1") || got["z"] != json.Number("0") {
		t.Errorf("got %v", got)
	}
	if len(Attrs(ctx)) != 3 {
		t.Errorf("Attrs(ctx) = %v", Attrs(ctx))
	}
	if FromContext(context.Background()) != slog.Default() {
		t.Error("FromContext without a logger must give slog.Default")
	}
	if FromContext(NewContext(ctx, log)) != log {
		t.Error("FromContext must give the logger NewContext set")
	}
}

func TestLevelFromEnvironment(t *testing.T) {
	log, buf := capture(t)
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("GOLDEN_LOG_LEVEL", "debug")
	log = New(testID, Writer(buf), Now(clock(fixedNow)))
	log.Debug("d")
	if len(lines(t, buf)) != 1 {
		t.Errorf("GOLDEN_LOG_LEVEL=debug must win over LOG_LEVEL=warn: %s", buf.String())
	}
	buf.Reset()
	t.Setenv("GOLDEN_LOG_LEVEL", "")
	log = New(testID, Writer(buf), Now(clock(fixedNow)))
	log.Info("i")
	log.Warn("w")
	ls := lines(t, buf)
	if len(ls) != 1 || ls[0]["body"] != "w" {
		t.Errorf("LOG_LEVEL=warn must drop info: %s", buf.String())
	}
}

func TestBadSettingsFallBackAndCheckFails(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")
	t.Setenv("LOG_OUTPUT", "")
	err := Check(testID)
	if !failure.Is(err, failure.Usage) || !strings.Contains(err.Error(), "LOG_LEVEL=verbose") {
		t.Errorf("Check = %v", err)
	}
	var buf bytes.Buffer
	log := New(testID, Writer(&buf), Now(clock(fixedNow)))
	log.Info("still info")
	ls := lines(t, &buf)
	if len(ls) != 2 || ls[0]["event_name"] != "log.fallback" || ls[1]["body"] != "still info" {
		t.Errorf("want log.fallback then the line at info: %s", buf.String())
	}
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GOLDEN_LOG_OUTPUT", "syslog")
	if err := Check(testID); !failure.Is(err, failure.Usage) || !strings.Contains(err.Error(), "GOLDEN_LOG_OUTPUT=syslog") {
		t.Errorf("Check = %v", err)
	}
}

func TestOutputSelection(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "")
	t.Setenv("GOLDEN_LOG_OUTPUT", "")
	t.Setenv("LOG_OUTPUT", "")
	s := Resolve(testID)
	if fileExists("/.dockerenv") || fileExists("/run/.containerenv") {
		if s.Output != OutputStdout {
			t.Errorf("inside a container: %+v", s)
		}
	} else if s.Output != OutputFile || s.OutputSource != "default" {
		t.Errorf("default: %+v", s)
	}
	if !strings.HasSuffix(s.Dir, "/golden/logs") && !strings.HasSuffix(s.Dir, "/Logs/golden") {
		t.Errorf("Dir = %q", s.Dir)
	}

	t.Setenv("LOG_OUTPUT", "stderr")
	if s = Resolve(testID); s.Output != OutputStderr || s.OutputSource != "LOG_OUTPUT" {
		t.Errorf("LOG_OUTPUT: %+v", s)
	}
	t.Setenv("GOLDEN_LOG_OUTPUT", "stdout")
	if s = Resolve(testID); s.Output != OutputStdout || s.OutputSource != "GOLDEN_LOG_OUTPUT" {
		t.Errorf("GOLDEN_LOG_OUTPUT must win: %+v", s)
	}

	t.Setenv("GOLDEN_LOG_OUTPUT", "")
	t.Setenv("LOG_OUTPUT", "")
	t.Setenv("JOURNAL_STREAM", "1:2")
	if journalMatchesStdout() {
		t.Error("JOURNAL_STREAM=1:2 must not match stdout")
	}
	if dev, ino, ok := stdoutDevIno(); ok {
		t.Setenv("JOURNAL_STREAM", dev+":"+ino)
		if s = Resolve(testID); s.Output != OutputStdout || s.OutputSource != "JOURNAL_STREAM matches stdout" {
			t.Errorf("JOURNAL_STREAM matching stdout: %+v", s)
		}
		t.Setenv("LOG_OUTPUT", "file")
		if s = Resolve(testID); s.Output != OutputFile {
			t.Errorf("LOG_OUTPUT=file must win over the journal: %+v", s)
		}
	}
}

func TestFileOpenFailureFallsBackToStderr(t *testing.T) {
	dir := t.TempDir() + "/not-a-dir"
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GOLDEN_LOG_LEVEL", "")
	out := captureStderr(t, func() {
		log := New(testID, Dir(dir), Output(OutputFile), Now(clock(fixedNow)))
		log.Info("after", "k", "v")
	})
	ls := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(ls) < 2 || !strings.Contains(ls[0], "WARN log.fallback") || !strings.Contains(out, "exception.message=mkdir") {
		t.Errorf("want a text log.fallback line first, got %q", out)
	}
	if !strings.Contains(ls[len(ls)-1], "INFO after k=v") {
		t.Errorf("want the line on stderr as text, got %q", out)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		b.ReadFrom(r)
		done <- b.String()
	}()
	fn()
	os.Stderr = old
	w.Close()
	return <-done
}

func TestTextRendering(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GOLDEN_LOG_LEVEL", "")
	oldLocal := time.Local
	time.Local = time.UTC
	defer func() { time.Local = oldLocal }()
	out := captureStderr(t, func() {
		log := New(testID, Output(OutputStderr), Now(clock(fixedNow)))
		ctx := WithAttrs(context.Background(), slog.String("caller.id", "s7"))
		log.InfoContext(ctx, "listening", "addr", ":8080", "note", "has space", "n", 3)
		log.LogAttrs(ctx, slog.LevelWarn, "slow", Event("job.progress"), slog.String("job.name", "reindex"),
			slog.String("detail", strings.Repeat("x", 81)))
		log.Error("sync failed", "err", stackErr{"boom"})
	})
	want := "14:03:11.482 INFO listening caller.id=s7 addr=:8080 note=\"has space\" n=3\n" +
		"14:03:11.482 WARN job.progress body=slow caller.id=s7 job.name=reindex\n" +
		"    detail=" + strings.Repeat("x", 81) + "\n" +
		"14:03:11.482 ERROR sync failed exception.type=logging.stackErr exception.message=boom\n" +
		"    exception.stacktrace=main.go:1\n        main.go:2\n"
	if out != want {
		t.Errorf("text rendering differs\n got: %q\nwant: %q", out, want)
	}
}

func stdoutDevIno() (dev, ino string, ok bool) {
	info, err := os.Stdout.Stat()
	if err != nil {
		return "", "", false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", false
	}
	return strconv.FormatUint(uint64(st.Dev), 10), strconv.FormatUint(uint64(st.Ino), 10), true
}
