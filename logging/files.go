package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// meta is what a sink needs to know about a line besides its bytes.
type meta struct {
	level slog.Level
	event string
}

// sink writes one serialized line.
type sink interface {
	write(b []byte, m meta)
}

// writerSink writes each line with one Write call: stdout, stderr, or a test writer.
type writerSink struct {
	w interface{ Write([]byte) (int, error) }
}

func (s writerSink) write(b []byte, _ meta) {
	s.w.Write(b)
}

// eventFormatter makes the line for an event the sink writes about itself.
type eventFormatter func(level slog.Level, event string, attrs ...slog.Attr) []byte

// fileSink appends lines to <dir>/<name>.<YYYY-MM-DD>.jsonl by the rules of standard.md
// section 5: date checked on every write, one write per line, retention, size cap, and a
// log.dropped count after failed writes.
type fileSink struct {
	dir      string
	name     string
	maxBytes int64
	keepDays int
	now      func() time.Time
	format   eventFormatter

	mu        sync.Mutex
	f         *os.File
	date      string
	capped    bool
	dropped   int
	lastSweep time.Time
}

// openFileSink opens today's file. It fails only when that first open fails; New then falls
// back to stderr.
func openFileSink(dir, name string, maxBytes int64, keepDays int, now func() time.Time, format eventFormatter) (*fileSink, error) {
	s := &fileSink{dir: dir, name: name, maxBytes: maxBytes, keepDays: keepDays, now: now, format: format}
	t := now()
	if err := s.rotate(t); err != nil {
		return nil, err
	}
	s.sweep(t)
	return s, nil
}

// Path returns the file of the day t.
func (s *fileSink) path(date string) string {
	return filepath.Join(s.dir, s.name+"."+date+".jsonl")
}

// rotate opens the file of t's UTC date when it is not the open one.
func (s *fileSink) rotate(t time.Time) error {
	date := t.UTC().Format("2006-01-02")
	if s.f != nil && date == s.date {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path(date), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if s.f != nil {
		s.f.Close()
	}
	s.f, s.date, s.capped = f, date, false
	return nil
}

func (s *fileSink) write(b []byte, m meta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	if err := s.rotate(t); err != nil {
		s.dropped++
		return
	}
	if t.Sub(s.lastSweep) >= 24*time.Hour {
		s.sweep(t)
	}
	if s.maxBytes > 0 {
		size := s.size()
		if size >= s.maxBytes+s.maxBytes/10 {
			return
		}
		if size >= s.maxBytes {
			if !s.capped {
				s.capped = true
				s.raw(s.format(slog.LevelWarn, "log.capped", slog.Int64("size_bytes", size)))
			}
			if !priority(m) {
				return
			}
		}
	}
	if s.dropped > 0 {
		n := s.dropped
		s.dropped = 0
		if !s.raw(s.format(slog.LevelWarn, "log.dropped", slog.Int("count", n))) {
			s.dropped += n
			return
		}
	}
	s.raw(b)
}

// priority reports whether a line still passes after the cap: process.* events and ERROR or
// worse.
func priority(m meta) bool {
	return m.level >= slog.LevelError || strings.HasPrefix(m.event, "process.")
}

func (s *fileSink) size() int64 {
	info, err := s.f.Stat()
	if err != nil {
		return 0
	}
	return info.Size()
}

// raw writes one line with one write call. A short write gets a newline, best effort, so
// the next line starts clean; the cut line counts as dropped.
func (s *fileSink) raw(b []byte) bool {
	n, err := s.f.Write(b)
	if err == nil && n == len(b) {
		return true
	}
	if n > 0 && n < len(b) {
		s.f.Write([]byte{'\n'})
	}
	s.dropped++
	return false
}

var datedName = regexp.MustCompile(`^(.*)\.(\d{4}-\d{2}-\d{2})\.jsonl$`)

// sweep deletes the sink's own files dated more than keepDays before t.
func (s *fileSink) sweep(t time.Time) {
	s.lastSweep = t
	if s.keepDays <= 0 {
		return
	}
	cutoff := t.UTC().AddDate(0, 0, -s.keepDays).Format("2006-01-02")
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		m := datedName.FindStringSubmatch(e.Name())
		if m == nil || m[1] != s.name || m[2] >= cutoff {
			continue
		}
		os.Remove(filepath.Join(s.dir, e.Name()))
	}
}
