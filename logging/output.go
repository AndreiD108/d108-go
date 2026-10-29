package logging

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/paths"
)

// The outputs of standard.md section 5. Stderr renders text; the other two write JSON lines.
const (
	OutputStdout = "stdout"
	OutputFile   = "file"
	OutputStderr = "stderr"
)

// Settings is what the environment asks of the logger, with the source of each choice, so
// doctor can print "where the logs go and why".
type Settings struct {
	// Output is stdout, file or stderr.
	Output string
	// OutputSource names what chose Output: the variable, the detection, or "default".
	OutputSource string
	// Level is the minimum level written.
	Level slog.Level
	// LevelSource names the variable that set Level, or "default".
	LevelSource string
	// Dir is the log folder used when Output is file.
	Dir string
	// Err is the usage failure for a variable with a value outside its set, or nil. New
	// ignores the bad value and says so once; cli and lifecycle refuse to start.
	Err error
}

// Resolve reads the environment for id by the order of standard.md section 5:
// <APP>_LOG_OUTPUT, LOG_OUTPUT, JOURNAL_STREAM matching stdout, a container marker file, then
// the file; <APP>_LOG_LEVEL, LOG_LEVEL, then info.
func Resolve(id identity.Identity) Settings {
	s := Settings{Output: OutputFile, OutputSource: "default", Level: slog.LevelInfo, LevelSource: "default"}
	s.Dir = paths.For(id.Name, paths.Prefix(id.EnvPrefix())).Logs

	for _, name := range []string{id.EnvPrefix() + "LOG_OUTPUT", "LOG_OUTPUT"} {
		v := os.Getenv(name)
		if v == "" {
			continue
		}
		switch v {
		case OutputStdout, OutputFile, OutputStderr:
			s.Output, s.OutputSource = v, name
		default:
			s.Err = failure.New(failure.Usage, name+"="+v+" is not one of stdout, file, stderr",
				"set "+name+" to stdout, file or stderr, or unset it", "variable", name, "value", v)
		}
		break
	}
	if s.OutputSource == "default" {
		switch {
		case journalMatchesStdout():
			s.Output, s.OutputSource = OutputStdout, "JOURNAL_STREAM matches stdout"
		case fileExists("/.dockerenv"):
			s.Output, s.OutputSource = OutputStdout, "/.dockerenv exists"
		case fileExists("/run/.containerenv"):
			s.Output, s.OutputSource = OutputStdout, "/run/.containerenv exists"
		}
	}

	for _, name := range []string{id.EnvPrefix() + "LOG_LEVEL", "LOG_LEVEL"} {
		v := os.Getenv(name)
		if v == "" {
			continue
		}
		if level, ok := parseLevel(v); ok {
			s.Level, s.LevelSource = level, name
		} else if s.Err == nil {
			s.Err = failure.New(failure.Usage, name+"="+v+" is not one of debug, info, warn, error",
				"set "+name+" to debug, info, warn or error, or unset it", "variable", name, "value", v)
		}
		break
	}
	return s
}

// Check returns the usage failure of Resolve, for cli and lifecycle to refuse a start on a
// bad LOG_LEVEL or LOG_OUTPUT.
func Check(id identity.Identity) error {
	return Resolve(id).Err
}

func parseLevel(v string) (slog.Level, bool) {
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return 0, false
}

// journalMatchesStdout reports whether systemd's JOURNAL_STREAM ("dev:inode") names the file
// behind stdout, which is the case for a service whose stdout is the journal.
func journalMatchesStdout() bool {
	dev, ino, ok := strings.Cut(os.Getenv("JOURNAL_STREAM"), ":")
	if !ok {
		return false
	}
	wantDev, err1 := strconv.ParseUint(dev, 10, 64)
	wantIno, err2 := strconv.ParseUint(ino, 10, 64)
	if err1 != nil || err2 != nil {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return uint64(st.Dev) == wantDev && uint64(st.Ino) == wantIno
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
