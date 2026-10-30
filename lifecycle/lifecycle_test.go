package lifecycle_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AndreiD108/d108-go/contracttest"
)

func TestMain(m *testing.M) {
	if name := os.Getenv(envScenario); name != "" {
		runScenario(name)
	}
	os.Exit(m.Run())
}

type line map[string]any

func (l line) str(key string) string {
	s, _ := l[key].(string)
	return s
}

func (l line) num(key string) float64 {
	f, _ := l[key].(float64)
	return f
}

type result struct {
	code    int
	lines   []line
	stderr  string
	elapsed time.Duration
}

// run runs the test binary as the scenario's child and reads its log file back. Every line
// must pass the contract check.
func run(t *testing.T, scenario string, env ...string) result {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(cleanEnv(), envScenario+"="+scenario, envLogDir+"="+dir, "LOG_OUTPUT=file")
	cmd.Env = append(cmd.Env, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	var exit *exec.ExitError
	code := 0
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("child: %v; stderr: %s", err, stderr.String())
	}
	return result{code: code, lines: readLines(t, dir), stderr: stderr.String(), elapsed: elapsed}
}

// cleanEnv keeps the environment without the variables that steer the logger or telemetry.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasSuffix(key, "LOG_LEVEL") || strings.HasSuffix(key, "LOG_OUTPUT") || strings.HasPrefix(key, "OTEL_") || strings.HasPrefix(key, "D108_LIFECYCLE_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func readLines(t *testing.T, dir string) []line {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []line
	var raws [][]byte
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
			if len(raw) == 0 {
				continue
			}
			raws = append(raws, append(raw, '\n'))
			var l line
			if err := json.Unmarshal(raw, &l); err != nil {
				t.Fatalf("bad line %q: %v", raw, err)
			}
			lines = append(lines, l)
		}
	}
	t.Run("contract", func(t *testing.T) { contracttest.Check(t, raws) })
	return lines
}

// events returns the event names in order.
func events(lines []line) []string {
	var names []string
	for _, l := range lines {
		if e := l.str("event_name"); e != "" {
			names = append(names, e)
		}
	}
	return names
}

// steps returns the step names of the "step ran" lines in order.
func steps(lines []line) []string {
	var names []string
	for _, l := range lines {
		if l.str("body") == "step ran" {
			names = append(names, l.str("step"))
		}
	}
	return names
}

func last(t *testing.T, lines []line) line {
	t.Helper()
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	return lines[len(lines)-1]
}

func find(lines []line, event string) (line, bool) {
	for _, l := range lines {
		if l.str("event_name") == event {
			return l, true
		}
	}
	return nil, false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCleanReturn(t *testing.T) {
	r := run(t, "clean")
	if r.code != 0 {
		t.Fatalf("exit %d; stderr %s", r.code, r.stderr)
	}
	if !equal(events(r.lines), []string{"process.start", "process.stop"}) {
		t.Errorf("events %v", events(r.lines))
	}
	if got := steps(r.lines); !equal(got, []string{"c", "b", "a"}) {
		t.Errorf("steps ran %v, want reverse order", got)
	}
	start := r.lines[0]
	if start.str("process.executable.path") == "" || start.str("process.runtime.name") != "go" || strings.HasPrefix(start.str("process.runtime.version"), "go") {
		t.Errorf("process.start attributes: %v", start)
	}
	if args := start.str("process.command_args"); !strings.HasPrefix(args, "[\"") || !strings.Contains(args, "-test.run=^$") {
		t.Errorf("process.command_args %q, want a JSON array string of os.Args", args)
	}
	stop := last(t, r.lines)
	if stop.str("reason") != "done" || stop.num("exit_code") != 0 || stop.num("stop_errors") != 0 || stop.str("severity_text") != "INFO" {
		t.Errorf("process.stop %v", stop)
	}
}

func TestSignals(t *testing.T) {
	for _, tc := range []struct {
		sig  string
		code int
	}{{"SIGTERM", 143}, {"SIGINT", 130}, {"SIGHUP", 129}} {
		t.Run(tc.sig, func(t *testing.T) {
			r := run(t, "signal", envSignal+"="+tc.sig)
			if r.code != tc.code {
				t.Fatalf("exit %d, want %d", r.code, tc.code)
			}
			stop := last(t, r.lines)
			if stop.str("event_name") != "process.stop" || stop.str("reason") != "signal" || stop.str("signal") != tc.sig || stop.num("exit_code") != float64(tc.code) {
				t.Errorf("process.stop %v", stop)
			}
			if !equal(steps(r.lines), []string{"a"}) {
				t.Errorf("steps %v", steps(r.lines))
			}
			for _, l := range r.lines {
				if l.str("severity_text") == "ERROR" {
					t.Errorf("the cancelled return must not be logged: %v", l)
				}
			}
		})
	}
}

func TestStopFromGoroutine(t *testing.T) {
	r := run(t, "stop")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	stop := last(t, r.lines)
	if stop.str("reason") != "app" || stop.num("exit_code") != 0 {
		t.Errorf("process.stop %v", stop)
	}
	var order []string
	for _, l := range r.lines {
		if b := l.str("body"); b == "worker done" || b == "step ran" {
			order = append(order, b)
		}
	}
	if !equal(order, []string{"worker done", "step ran"}) {
		t.Errorf("the steps must run after the Go goroutines ended: %v", order)
	}
}

func TestFailingSteps(t *testing.T) {
	r := run(t, "failstep")
	if r.code != 5 {
		t.Fatalf("exit %d, want 5 (not_found)", r.code)
	}
	if got := steps(r.lines); !equal(got, []string{"b", "a"}) {
		t.Errorf("steps ran %v; c panics before it logs", got)
	}
	var failed []string
	for _, l := range r.lines {
		if l.str("body") == "stop step failed" {
			failed = append(failed, l.str("step"))
			if l.str("severity_text") != "ERROR" || l.str("exception.message") == "" {
				t.Errorf("step failure line %v", l)
			}
			if l.str("step") == "c" && (l.str("exception.type") != "work.PanicError" || l.str("exception.stacktrace") == "") {
				t.Errorf("panic in a step must be a PanicError with a stack: %v", l)
			}
		}
	}
	if !equal(failed, []string{"c", "b"}) {
		t.Errorf("failed steps %v", failed)
	}
	stop := last(t, r.lines)
	if stop.str("reason") != "error" || stop.num("stop_errors") != 2 || stop.num("exit_code") != 5 || stop.str("error.type") != "not_found" || stop.str("exception.message") != "not_found: no index" || stop.str("severity_text") != "ERROR" {
		t.Errorf("process.stop %v", stop)
	}
}

func TestHangingStepIsForced(t *testing.T) {
	r := run(t, "hang")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	if r.elapsed > 3*time.Second {
		t.Errorf("took %v; grace was 200 ms", r.elapsed)
	}
	stop := last(t, r.lines)
	if stop.str("event_name") != "process.stop" || stop.str("reason") != "forced" || stop.str("step") != "slow" || stop.str("phase") != "steps" || stop.str("severity_text") != "WARN" {
		t.Errorf("process.stop %v", stop)
	}
	if got := steps(r.lines); len(got) != 0 {
		t.Errorf("step a must not run after the forced exit: %v", got)
	}
}

func TestPanicInGo(t *testing.T) {
	r := run(t, "gopanic")
	if r.code != 1 {
		t.Fatalf("exit %d, want 1", r.code)
	}
	crash := last(t, r.lines)
	if crash.str("event_name") != "process.crash" || crash.str("severity_text") != "FATAL" || crash.str("exception.type") != "work.PanicError" || crash.str("exception.message") != "panic: boom" || crash.str("exception.stacktrace") == "" || crash.str("goroutine") != "worker" || crash.num("exit_code") != 1 {
		t.Errorf("process.crash %v", crash)
	}
	if _, ok := crash["reason"]; ok {
		t.Errorf("process.crash carries no reason: %v", crash)
	}
	if !equal(steps(r.lines), []string{"a"}) {
		t.Errorf("the steps must still run: %v", steps(r.lines))
	}
}

func TestPanicInFunc(t *testing.T) {
	r := run(t, "fnpanic")
	if r.code != 1 {
		t.Fatalf("exit %d, want 1", r.code)
	}
	crash := last(t, r.lines)
	if crash.str("event_name") != "process.crash" || crash.str("exception.message") != "panic: fn boom" {
		t.Errorf("process.crash %v", crash)
	}
	if _, ok := crash["goroutine"]; ok {
		t.Errorf("a panic in fn names no goroutine: %v", crash)
	}
	if !equal(steps(r.lines), []string{"a"}) {
		t.Errorf("steps %v", steps(r.lines))
	}
}

func TestGoroutineError(t *testing.T) {
	r := run(t, "goerror")
	if r.code != 7 {
		t.Fatalf("exit %d, want 7 (remote)", r.code)
	}
	stop := last(t, r.lines)
	if stop.str("reason") != "error" || stop.str("goroutine") != "worker" || stop.str("error.type") != "remote" || stop.str("exception.message") != "remote: upstream gone" {
		t.Errorf("process.stop %v", stop)
	}
}

func TestSecondSignalForces(t *testing.T) {
	r := run(t, "doublesigint")
	if r.code != 130 {
		t.Fatalf("exit %d, want 130", r.code)
	}
	if r.elapsed > 3*time.Second {
		t.Errorf("took %v; the second SIGINT must force at once", r.elapsed)
	}
	stop := last(t, r.lines)
	if stop.str("reason") != "forced" || stop.str("signal") != "SIGINT" || stop.str("step") != "slow" {
		t.Errorf("process.stop %v", stop)
	}
}

func TestStopLineAfterCap(t *testing.T) {
	r := run(t, "cap")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	if _, ok := find(r.lines, "log.capped"); !ok {
		t.Fatal("the cap was not reached; the scenario must write past it")
	}
	if stop := last(t, r.lines); stop.str("event_name") != "process.stop" {
		t.Errorf("last line %v", stop)
	}
}

func TestLateErrorIsLoggedNotExitCode(t *testing.T) {
	r := run(t, "lateerror")
	if r.code != 0 {
		t.Fatalf("exit %d; Stop sets the code, the later error does not", r.code)
	}
	var found bool
	for _, l := range r.lines {
		if l.str("body") == "error after the stop started" && l.str("exception.message") == "listener broke" && l.str("severity_text") == "ERROR" {
			found = true
		}
	}
	if !found {
		t.Errorf("the late error must be an ERROR line: %v", r.lines)
	}
	if stop := last(t, r.lines); stop.str("reason") != "app" {
		t.Errorf("process.stop %v", stop)
	}
}

func TestLateOnStopDoesNotRun(t *testing.T) {
	r := run(t, "late-onstop")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	if got := steps(r.lines); len(got) != 0 {
		t.Errorf("a step registered after the stop started ran: %v", got)
	}
	var warned bool
	for _, l := range r.lines {
		if l.str("severity_text") == "WARN" && l.str("step") == "late" {
			warned = true
		}
	}
	if !warned {
		t.Error("no WARN line for the late step")
	}
}

func TestLatePanicIsACrash(t *testing.T) {
	r := run(t, "latepanic")
	if r.code != 1 {
		t.Fatalf("exit %d, want 1: a panic after Stop is still a crash", r.code)
	}
	crash := last(t, r.lines)
	if crash.str("event_name") != "process.crash" || crash.str("exception.message") != "panic: after stop" {
		t.Errorf("last line %v", crash)
	}
}

func TestGoAfterDrainDoesNotRun(t *testing.T) {
	r := run(t, "lategoroutine")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	var warned bool
	for _, l := range r.lines {
		if l.str("body") == "late ran" {
			t.Error("the late goroutine ran")
		}
		if l.str("severity_text") == "WARN" && l.str("goroutine") == "late" {
			warned = true
		}
	}
	if !warned {
		t.Error("no WARN line for the late goroutine")
	}
}

func TestForcedLineCountsFailedSteps(t *testing.T) {
	r := run(t, "hangafterfail")
	stop := last(t, r.lines)
	if stop.str("reason") != "forced" || stop.num("stop_errors") != 1 || stop.str("step") != "slow" {
		t.Errorf("process.stop %v", stop)
	}
}

func TestForcingSignalSetsTheCode(t *testing.T) {
	r := run(t, "termthenint")
	if r.code != 130 {
		t.Fatalf("exit %d, want 130: the forcing SIGINT wins over the first SIGTERM", r.code)
	}
	stop := last(t, r.lines)
	if stop.str("reason") != "forced" || stop.str("signal") != "SIGTERM" || stop.num("exit_code") != 130 {
		t.Errorf("process.stop %v", stop)
	}
}

func TestFlushIsBounded(t *testing.T) {
	r := run(t, "flush", "OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:1")
	if r.code != 0 {
		t.Fatalf("exit %d; stderr %s", r.code, r.stderr)
	}
	if r.elapsed < 1500*time.Millisecond || r.elapsed > 6*time.Second {
		t.Errorf("took %v; the flush bound is 2 s when the grace remainder is below it", r.elapsed)
	}
	if stop := last(t, r.lines); stop.str("event_name") != "process.stop" {
		t.Errorf("process.stop must stay the last line: %v", stop)
	}
}

func TestSignalCutsTheFlushAfterADeadlineForce(t *testing.T) {
	r := run(t, "forcedflush", "OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:1")
	if r.code != 130 {
		t.Fatalf("exit %d, want 130: the signal that cut the flush sets the code", r.code)
	}
	if r.elapsed > 2500*time.Millisecond {
		t.Errorf("took %v; the SIGINT at 600 ms must cut the 2 s flush (uncut runs take 3 s or more)", r.elapsed)
	}
	if stop := last(t, r.lines); stop.str("reason") != "forced" || stop.str("step") != "slow" || stop.num("exit_code") != 0 {
		t.Errorf("process.stop %v; the line keeps the sequence's code", stop)
	}
}

func TestBadEnvRefusesToStart(t *testing.T) {
	r := run(t, "clean", "LOG_LEVEL=loud")
	if r.code != 2 {
		t.Fatalf("exit %d, want 2", r.code)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(r.stderr), &envelope); err != nil || envelope["class"] != "usage" {
		t.Errorf("stderr %q, want the usage envelope", r.stderr)
	}
	if _, ok := find(r.lines, "process.start"); ok {
		t.Error("process.start must not be written on a refused start")
	}
}

func TestRunSetsTheDefaultLogger(t *testing.T) {
	r := run(t, "defaultlog")
	if r.code != 0 {
		t.Fatalf("exit %d; stderr %s", r.code, r.stderr)
	}
	if len(r.lines) != 3 || r.lines[1].str("body") != "through the default logger" || r.lines[1].str("k") != "v" || r.lines[1].str("service.name") != "lifecycle-test" {
		t.Errorf("lines %v, want the default logger's line between process.start and process.stop", r.lines)
	}
}
