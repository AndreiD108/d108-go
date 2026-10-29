// Package doctor builds the report behind every tool's `doctor` subcommand (standard.md
// section 9.2): where the log lines go and why, the level and its source, the current log
// file, whether the agent answers on 127.0.0.1:4317, whether tracing is on, the version and
// the instance id. Other packages add sections with Add: config adds the effective settings
// with their sources, secrets the names of the stored secrets.
package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/internal/otelres"
	"github.com/AndreiD108/d108-go/logging"
)

// AgentAddr is the local collector endpoint doctor probes with a TCP connect.
const AgentAddr = "127.0.0.1:4317"

// Item is one line of the report: a key, its value, where the value came from, and what to
// do about it when it is not what the reader wants.
type Item struct {
	Key    string `json:"-"`
	Value  string `json:"value"`
	Source string `json:"source,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Section is a named group of items, in the order they were added.
type Section struct {
	Name  string
	Items []Item
}

// Report is the whole report, sections in order: program, logging, telemetry, then the
// sections added with Add in registration order.
type Report struct {
	Sections []Section
}

var (
	mu    sync.Mutex
	added []Section
	fns   []func() []Item
)

// Add registers a section another package fills when the report is built. A second Add with
// the same name replaces the first. fn runs on every Report call.
func Add(section string, fn func() []Item) {
	mu.Lock()
	defer mu.Unlock()
	for i, s := range added {
		if s.Name == section {
			fns[i] = fn
			return
		}
	}
	added = append(added, Section{Name: section})
	fns = append(fns, fn)
}

// Build makes the report for id. The logging section describes the logger in ctx
// (logging.FromContext) when it is one of logging.New, else what the environment asks for.
// The agent probe uses ctx with a one second bound.
func Build(ctx context.Context, id identity.Identity) *Report {
	settings, ok := logging.SettingsOf(logging.FromContext(ctx))
	if !ok {
		settings = logging.Resolve(id)
	}
	r := &Report{Sections: []Section{program(id), loggingSection(id, settings), telemetry(ctx)}}
	mu.Lock()
	defer mu.Unlock()
	for i, s := range added {
		r.Sections = append(r.Sections, Section{Name: s.Name, Items: fns[i]()})
	}
	return r
}

func program(id identity.Identity) Section {
	return Section{Name: "program", Items: []Item{
		{Key: "name", Value: id.Name},
		{Key: "version", Value: id.Version, Source: versionSource(id.Version)},
		{Key: "instance_id", Value: id.InstanceID, Note: "service.instance.id of this run; its lines carry it"},
		{Key: "pid", Value: strconv.Itoa(id.PID)},
	}}
}

func versionSource(v string) string {
	if v == "dev" {
		return "no ldflags version and no build info"
	}
	return "build"
}

func loggingSection(id identity.Identity, s logging.Settings) Section {
	prefix := id.EnvPrefix()
	items := []Item{
		{Key: "output", Value: s.Output, Source: s.OutputSource,
			Note: "set " + prefix + "LOG_OUTPUT or LOG_OUTPUT to stdout, file or stderr"},
		{Key: "level", Value: strings.ToLower(s.Level.String()), Source: s.LevelSource,
			Note: "set " + prefix + "LOG_LEVEL or LOG_LEVEL to debug, info, warn or error"},
	}
	if s.Output == logging.OutputFile {
		file := filepath.Join(s.Dir, id.Name+"."+time.Now().UTC().Format("2006-01-02")+".jsonl")
		items = append(items, Item{Key: "file", Value: file, Note: "today's file (UTC date); the agent reads this folder"})
	}
	if s.Err != nil {
		items = append(items, Item{Key: "error", Value: s.Err.Error(), Note: "the tool refuses to start until this is fixed"})
	}
	return Section{Name: "logging", Items: items}
}

func telemetry(ctx context.Context) Section {
	agent := Item{Key: "agent", Value: "reachable", Source: "TCP connect to " + AgentAddr}
	if err := probe(ctx, AgentAddr); err != nil {
		agent.Value = "not reachable"
		agent.Note = "no collector on " + AgentAddr + ": log files stay on this machine, traces and metrics are dropped; install the agent (d108/agent/README.md). " + err.Error()
	}
	tracing := Item{Key: "tracing", Value: "off", Source: otelres.EndpointVar + " unset",
		Note: "set " + otelres.EndpointVar + "=http://" + AgentAddr + " to send spans and metrics to the agent"}
	if endpoint := os.Getenv(otelres.EndpointVar); endpoint != "" {
		tracing = Item{Key: "tracing", Value: "on", Source: otelres.EndpointVar + "=" + endpoint}
	}
	return Section{Name: "telemetry", Items: []Item{agent, tracing}}
}

// probe opens one TCP connection to addr, bounded by ctx and one second.
func probe(ctx context.Context, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// Text renders the report for a terminal: a section name, then its items as key, value,
// source in columns, and the note on its own indented line.
func (r *Report) Text() string {
	var b strings.Builder
	for i, s := range r.Sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s.Name + "\n")
		keyWidth, valueWidth := 0, 0
		for _, it := range s.Items {
			keyWidth = max(keyWidth, len(it.Key))
			valueWidth = max(valueWidth, len(it.Value))
		}
		for _, it := range s.Items {
			fmt.Fprintf(&b, "  %-*s  %s", keyWidth, it.Key, it.Value)
			if it.Source != "" {
				fmt.Fprintf(&b, "%*s  (%s)", valueWidth-len(it.Value), "", it.Source)
			}
			b.WriteString("\n")
			if it.Note != "" {
				fmt.Fprintf(&b, "  %*s  %s\n", keyWidth, "", it.Note)
			}
		}
	}
	return b.String()
}

// MarshalJSON writes {"section": {"key": {"value", "source", "note"}}} with sections and keys
// in report order.
func (r *Report) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, s := range r.Sections {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeKey(&buf, s.Name); err != nil {
			return nil, err
		}
		buf.WriteByte('{')
		for j, it := range s.Items {
			if j > 0 {
				buf.WriteByte(',')
			}
			if err := writeKey(&buf, it.Key); err != nil {
				return nil, err
			}
			v, err := json.Marshal(it)
			if err != nil {
				return nil, err
			}
			buf.Write(v)
		}
		buf.WriteByte('}')
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func writeKey(buf *bytes.Buffer, key string) error {
	k, err := json.Marshal(key)
	if err != nil {
		return err
	}
	buf.Write(k)
	buf.WriteByte(':')
	return nil
}
