package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AndreiD108/d108-go/contracttest"
	"github.com/AndreiD108/d108-go/identity"
)

const childEnv = "D108_LOGGING_CHILD_DIR"

func TestMain(m *testing.M) {
	if dir := os.Getenv(childEnv); dir != "" {
		childWriter(dir)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fileLogger returns a logger writing dated files in a temp dir with a settable clock.
func fileLogger(t *testing.T, now *time.Time, opts ...Option) (*slog.Logger, string) {
	t.Helper()
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GOLDEN_LOG_LEVEL", "")
	dir := t.TempDir()
	opts = append([]Option{Output(OutputFile), Dir(dir), Now(func() time.Time { return *now })}, opts...)
	return New(testID, opts...), dir
}

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.Write(raw)
	return lines(t, &buf)
}

func TestDateRolloverAndModes(t *testing.T) {
	now := time.Date(2026, 10, 27, 23, 59, 59, 0, time.UTC)
	log, dir := fileLogger(t, &now)
	log.Info("before")
	now = now.Add(2 * time.Second)
	log.Info("after")
	day1 := readLines(t, filepath.Join(dir, "golden.2026-10-27.jsonl"))
	day2 := readLines(t, filepath.Join(dir, "golden.2026-10-28.jsonl"))
	if len(day1) != 1 || day1[0]["body"] != "before" || len(day2) != 1 || day2[0]["body"] != "after" {
		t.Errorf("rollover: day1 %v, day2 %v", day1, day2)
	}
	info, _ := os.Stat(filepath.Join(dir, "golden.2026-10-28.jsonl"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %o", info.Mode().Perm())
	}
	sub := filepath.Join(dir, "sub")
	New(testID, Output(OutputFile), Dir(sub), Now(func() time.Time { return now })).Info("x")
	dirInfo, _ := os.Stat(sub)
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %o", dirInfo.Mode().Perm())
	}
}

func TestSizeCap(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	log, dir := fileLogger(t, &now, MaxFileBytes(10000))
	path := filepath.Join(dir, "golden.2026-10-27.jsonl")
	for i := 0; i < 100; i++ {
		log.Info("fill", "i", i)
	}
	ls := readLines(t, path)
	capped := 0
	for _, l := range ls {
		if l["event_name"] == "log.capped" {
			capped++
			if _, ok := l["size_bytes"]; !ok {
				t.Error("log.capped without size_bytes")
			}
		}
	}
	if capped != 1 {
		t.Fatalf("log.capped written %d times", capped)
	}
	last := ls[len(ls)-1]
	if last["event_name"] != "log.capped" {
		t.Errorf("after the cap an info line got through: %v", last)
	}

	log.Error("still written")
	log.LogAttrs(context.Background(), slog.LevelInfo, "", Event("process.stop"))
	log.Info("not written")
	ls = readLines(t, path)
	if n := len(ls); ls[n-2]["body"] != "still written" || ls[n-1]["event_name"] != "process.stop" {
		t.Errorf("ERROR and process.* must pass after the cap: %v", ls[n-2:])
	}
	for i := 0; i < 40; i++ {
		log.Error("flood", "pad", strings.Repeat("x", 100))
	}
	info, _ := os.Stat(path)
	if info.Size() > 10000+1000+300 {
		t.Errorf("errors kept going past the further 10 %%: %d bytes", info.Size())
	}
	before := info.Size()
	log.Error("over")
	info, _ = os.Stat(path)
	if info.Size() != before {
		t.Error("a line was written past the further 10 %")
	}

	now = now.Add(24 * time.Hour)
	log.Info("new day")
	next := readLines(t, filepath.Join(dir, "golden.2026-10-28.jsonl"))
	if len(next) != 1 || next[0]["body"] != "new day" {
		t.Errorf("the cap must reset on the next day: %v", next)
	}
}

func TestDroppedCount(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	var got []byte
	s, err := openFileSink(dir, "golden", 0, 14, func() time.Time { return now }, func(level slog.Level, event string, attrs ...slog.Attr) []byte {
		b, _ := json.Marshal(map[string]any{"event_name": event, "count": attrs[0].Value.Int64()})
		got = append(b, '\n')
		return got
	})
	if err != nil {
		t.Fatal(err)
	}
	s.f.Close()
	s.write([]byte("lost 1\n"), meta{})
	s.write([]byte("lost 2\n"), meta{})
	if s.dropped != 2 {
		t.Fatalf("dropped = %d", s.dropped)
	}
	now = now.Add(24 * time.Hour)
	s.write([]byte("{\"body\":\"ok\"}\n"), meta{})
	raw, _ := os.ReadFile(filepath.Join(dir, "golden.2026-10-28.jsonl"))
	if !strings.HasPrefix(string(raw), `{"count":2,"event_name":"log.dropped"}`+"\n") || !strings.HasSuffix(string(raw), "\"ok\"}\n") {
		t.Errorf("want log.dropped count 2 then the line, got %q", raw)
	}
	if s.dropped != 0 {
		t.Errorf("dropped = %d after a successful write", s.dropped)
	}
}

func TestRetention(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	for _, name := range []string{"golden.2026-10-01.jsonl", "golden.2026-10-12.jsonl", "golden.2026-10-13.jsonl", "other.2026-10-01.jsonl", "golden.notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GOLDEN_LOG_LEVEL", "")
	New(testID, Output(OutputFile), Dir(dir), Now(func() time.Time { return now }), KeepDays(14))
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := "golden.2026-10-13.jsonl golden.2026-10-27.jsonl golden.notes.txt other.2026-10-01.jsonl"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("after retention: %s\nwant: %s", got, want)
	}
}

func TestConcurrentGoroutines(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	log, dir := fileLogger(t, &now, MaxFileBytes(0))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 300; i++ {
				log.Info("line", "g", g, "i", i, "pad", strings.Repeat("p", payloadSize(r)))
			}
		}(g)
	}
	wg.Wait()
	checkFile(t, filepath.Join(dir, "golden.2026-10-27.jsonl"), 8*300)
}

func TestConcurrentProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns 8 processes")
	}
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), childEnv+"="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- err
				t.Logf("child: %s", out)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	checkFile(t, filepath.Join(dir, "golden."+time.Now().UTC().Format("2006-01-02")+".jsonl"), 8*300)
}

// childWriter is the body of a child process: its own logger, 300 lines of mixed sizes.
func childWriter(dir string) {
	id := identity.New("golden")
	log := New(id, Output(OutputFile), Dir(dir), MaxFileBytes(0))
	r := rand.New(rand.NewSource(int64(os.Getpid())))
	for i := 0; i < 300; i++ {
		log.Info("line", "i", i, "pad", strings.Repeat("p", payloadSize(r)))
	}
}

// payloadSize is mostly small, one in four up to 70 KB, as the 2026-10-26 experiment.
func payloadSize(r *rand.Rand) int {
	if r.Intn(4) == 0 {
		return r.Intn(70000)
	}
	return r.Intn(1000)
}

// checkFile counts the lines of path and checks them against the contract.
func checkFile(t *testing.T, path string, want int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines [][]byte
	for _, l := range bytes.SplitAfter(raw, []byte("\n")) {
		if len(l) > 0 {
			lines = append(lines, l)
		}
	}
	if len(lines) != want {
		t.Errorf("%d lines, want %d", len(lines), want)
	}
	t.Run("contract", func(t *testing.T) { contracttest.Check(t, lines) })
}

func TestCapHoldsAcrossReopen(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GOLDEN_LOG_LEVEL", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "golden.2026-10-27.jsonl")
	open := func() *slog.Logger {
		return New(testID, Output(OutputFile), Dir(dir), Now(func() time.Time { return now }), MaxFileBytes(10000))
	}
	log := open()
	for i := 0; i < 200; i++ {
		log.Error("flood", "pad", strings.Repeat("x", 100))
	}
	info, _ := os.Stat(path)
	size := info.Size()
	if size < 10000 || size > 11000+300 {
		t.Fatalf("test needs an exhausted file, got %d bytes", size)
	}
	for i := 0; i < 5; i++ {
		log = open()
		log.Error("after restart")
		log.LogAttrs(context.Background(), slog.LevelInfo, "", Event("process.start"))
	}
	info, _ = os.Stat(path)
	if info.Size() != size {
		t.Errorf("an exhausted file grew on reopen: %d -> %d", size, info.Size())
	}
}

func TestPartialWriteGetsNewlineAndCount(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s, err := openFileSink(dir, "golden", 0, 0, func() time.Time { return now }, func(level slog.Level, event string, attrs ...slog.Attr) []byte {
		return []byte(`{"event_name":"` + event + `","count":` + strconv.FormatInt(attrs[0].Value.Int64(), 10) + "}\n")
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "golden.2026-10-27.jsonl")
	s.raw([]byte(`{"body":"first"}` + "\n"))
	// Simulate the short write: write a cut line directly, then account for it as raw does.
	n, _ := s.f.Write([]byte(`{"body":"cu`))
	if n > 0 {
		s.f.Write([]byte{'\n'})
	}
	s.dropped++
	s.write([]byte(`{"body":"third"}`+"\n"), meta{})
	raw, _ := os.ReadFile(path)
	want := `{"body":"first"}` + "\n" + `{"body":"cu` + "\n" + `{"event_name":"log.dropped","count":1}` + "\n" + `{"body":"third"}` + "\n"
	if string(raw) != want {
		t.Errorf("got %q\nwant %q", raw, want)
	}
}

func TestRetentionRunsDaily(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	for _, name := range []string{"golden.2026-10-13.jsonl", "other.2026-10-01.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GOLDEN_LOG_LEVEL", "")
	log := New(testID, Output(OutputFile), Dir(dir), Now(func() time.Time { return now }), KeepDays(14))
	log.Info("day 1")
	if _, err := os.Stat(filepath.Join(dir, "golden.2026-10-13.jsonl")); err != nil {
		t.Fatal("a file exactly KeepDays old must stay")
	}
	now = now.Add(23 * time.Hour)
	log.Info("still day 1 sweep")
	if _, err := os.Stat(filepath.Join(dir, "golden.2026-10-13.jsonl")); err != nil {
		t.Fatal("the sweep must not run before 24 h")
	}
	now = now.Add(2 * time.Hour)
	log.Info("day 2")
	if _, err := os.Stat(filepath.Join(dir, "golden.2026-10-13.jsonl")); err == nil {
		t.Error("the daily sweep must remove the expired file")
	}
	if _, err := os.Stat(filepath.Join(dir, "other.2026-10-01.jsonl")); err != nil {
		t.Error("another service's file must stay")
	}
}
