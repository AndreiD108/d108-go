package doctor

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/internal/otelres"
	"github.com/AndreiD108/d108-go/logging"
)

var id = identity.Identity{Name: "tool", Version: "1.2.3", InstanceID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", PID: 42}

func section(r *Report, name string) map[string]Item {
	for _, s := range r.Sections {
		if s.Name == name {
			m := map[string]Item{}
			for _, it := range s.Items {
				m[it.Key] = it
			}
			return m
		}
	}
	return nil
}

func TestLoggingSectionFollowsTheEnvironment(t *testing.T) {
	t.Setenv("TOOL_LOG_OUTPUT", "stderr")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("TOOL_LOG_LEVEL", "")
	s := section(Build(t.Context(), id), "logging")
	if s["output"].Value != "stderr" || s["output"].Source != "TOOL_LOG_OUTPUT" {
		t.Errorf("output %+v", s["output"])
	}
	if s["level"].Value != "debug" || s["level"].Source != "LOG_LEVEL" {
		t.Errorf("level %+v", s["level"])
	}
	if _, ok := s["file"]; ok {
		t.Error("file item with output stderr")
	}
	if !strings.Contains(s["level"].Note, "TOOL_LOG_LEVEL") {
		t.Errorf("level note %q", s["level"].Note)
	}
}

func TestFileItemNamesTodaysFile(t *testing.T) {
	t.Setenv("TOOL_LOG_OUTPUT", "file")
	dir := t.TempDir()
	t.Setenv("TOOL_STATE_DIR", dir)
	s := section(Build(t.Context(), id), "logging")
	file := s["file"].Value
	if !strings.HasPrefix(file, dir) || !strings.HasSuffix(file, ".jsonl") || !strings.Contains(file, "/logs/tool.20") {
		t.Errorf("file %q", file)
	}
}

func TestLoggerInContextWins(t *testing.T) {
	t.Setenv("TOOL_LOG_OUTPUT", "stdout")
	dir := t.TempDir()
	log := logging.New(id, logging.Output(logging.OutputFile), logging.Dir(dir), logging.Level(slog.LevelWarn))
	s := section(Build(logging.NewContext(t.Context(), log), id), "logging")
	if s["output"].Value != "file" || s["output"].Source != "logging.Output option" {
		t.Errorf("output %+v", s["output"])
	}
	if s["level"].Value != "warn" || s["level"].Source != "logging.Level option" {
		t.Errorf("level %+v", s["level"])
	}
	if !strings.HasPrefix(s["file"].Value, dir+"/tool.") {
		t.Errorf("file %+v", s["file"])
	}
}

func TestFallbackIsReported(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log := logging.New(id, logging.Output(logging.OutputFile), logging.Dir(filepath.Join(file, "logs")))
	s := section(Build(logging.NewContext(t.Context(), log), id), "logging")
	if s["output"].Value != "stderr" || !strings.HasPrefix(s["output"].Source, "fallback: cannot open the log file") {
		t.Errorf("output %+v", s["output"])
	}
}

func TestBadSettingIsAnItem(t *testing.T) {
	t.Setenv("TOOL_LOG_LEVEL", "loud")
	s := section(Build(t.Context(), id), "logging")
	if !strings.Contains(s["error"].Value, "loud") {
		t.Errorf("error item %+v", s["error"])
	}
}

func TestTelemetry(t *testing.T) {
	t.Setenv(otelres.EndpointVar, "")
	s := section(Build(t.Context(), id), "telemetry")
	if s["tracing"].Value != "off" || !strings.Contains(s["tracing"].Note, otelres.EndpointVar) {
		t.Errorf("tracing %+v", s["tracing"])
	}
	t.Setenv(otelres.EndpointVar, "http://127.0.0.1:4317")
	s = section(Build(t.Context(), id), "telemetry")
	if s["tracing"].Value != "on" || s["tracing"].Source != otelres.EndpointVar+"=http://127.0.0.1:4317" {
		t.Errorf("tracing %+v", s["tracing"])
	}
}

func TestProbe(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := probe(t.Context(), l.Addr().String()); err != nil {
		t.Errorf("listener not reachable: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	if err := probe(t.Context(), addr); err == nil {
		t.Error("closed port reachable")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := probe(ctx, addr); err == nil {
		t.Error("cancelled ctx reachable")
	}
}

func TestAddSectionsInOrderAndReplace(t *testing.T) {
	t.Cleanup(func() { added, fns = nil, nil })
	Add("settings", func() []Item { return []Item{{Key: "workers", Value: "4", Source: "default"}} })
	Add("secrets", func() []Item { return []Item{{Key: "token", Value: "stored"}} })
	Add("settings", func() []Item { return []Item{{Key: "workers", Value: "8", Source: "env"}} })
	r := Build(t.Context(), id)
	names := make([]string, len(r.Sections))
	for i, s := range r.Sections {
		names[i] = s.Name
	}
	if strings.Join(names, " ") != "program logging telemetry settings secrets" {
		t.Errorf("sections %v", names)
	}
	if section(r, "settings")["workers"].Value != "8" {
		t.Errorf("replaced section %+v", section(r, "settings"))
	}
}

func TestTextAndJSON(t *testing.T) {
	r := &Report{Sections: []Section{
		{Name: "program", Items: []Item{{Key: "name", Value: "tool"}, {Key: "version", Value: "1.2.3", Source: "build"}}},
		{Name: "logging", Items: []Item{{Key: "output", Value: "file", Source: "default", Note: "set LOG_OUTPUT"}}},
	}}
	want := "program\n" +
		"  name     tool\n" +
		"  version  1.2.3  (build)\n" +
		"\n" +
		"logging\n" +
		"  output  file  (default)\n" +
		"          set LOG_OUTPUT\n"
	if got := r.Text(); got != want {
		t.Errorf("text:\n%s\nwant:\n%s", got, want)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"program":{"name":{"value":"tool"},"version":{"value":"1.2.3","source":"build"}},` +
		`"logging":{"output":{"value":"file","source":"default","note":"set LOG_OUTPUT"}}}`
	if string(raw) != wantJSON {
		t.Errorf("json %s", raw)
	}
}
