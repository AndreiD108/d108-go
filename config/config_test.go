package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
)

type sample struct {
	Workers int               `json:"workers" env:"WORKERS, default=4"`
	Name    string            `json:"name" env:"NAME"`
	DryRun  bool              `json:"dry_run" env:"DRY_RUN, default=true"`
	Token   string            `json:"token" env:"TOKEN" secret:"true"`
	Labels  map[string]string `json:"labels" env:"LABELS"`
	Sync    sync              `json:"sync" env:", prefix=SYNC_"`
	Ignored string
}

type sync struct {
	Interval time.Duration `json:"interval" env:"INTERVAL, default=5m"`
	Channels []string      `json:"channels" env:"CHANNELS"`
}

var id = identity.Identity{Name: "sample-tool", Version: "dev", InstanceID: "01TEST", PID: 1}

// setup gives the test its own Config directory and writes file there when it is not empty.
func setup(t *testing.T, file string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SAMPLE_TOOL_CONFIG_DIR", dir)
	path := filepath.Join(dir, FileName)
	if file != "" {
		if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		env    map[string]string
		check  func(t *testing.T, cfg sample)
		source map[string]string
	}{
		{
			name: "neither: defaults",
			check: func(t *testing.T, cfg sample) {
				if cfg.Workers != 4 || !cfg.DryRun || cfg.Sync.Interval != 5*time.Minute || cfg.Name != "" {
					t.Errorf("got %+v", cfg)
				}
			},
			source: map[string]string{"workers": "default", "name": "default", "sync.interval": "default"},
		},
		{
			name: "file only",
			file: `{"workers": 8, "name": "a", "dry_run": false, "sync": {"interval": "15m", "channels": ["general", "ops"]}}`,
			check: func(t *testing.T, cfg sample) {
				if cfg.Workers != 8 || cfg.Name != "a" || cfg.DryRun || cfg.Sync.Interval != 15*time.Minute ||
					strings.Join(cfg.Sync.Channels, "|") != "general|ops" {
					t.Errorf("got %+v", cfg)
				}
			},
			source: map[string]string{"workers": "file", "dry_run": "file", "sync.interval": "file", "sync.channels": "file"},
		},
		{
			name: "env only",
			env:  map[string]string{"SAMPLE_TOOL_WORKERS": "2", "SAMPLE_TOOL_SYNC_CHANNELS": "x, y"},
			check: func(t *testing.T, cfg sample) {
				if cfg.Workers != 2 || strings.Join(cfg.Sync.Channels, "|") != "x|y" || cfg.Sync.Interval != 5*time.Minute {
					t.Errorf("got %+v", cfg)
				}
			},
			source: map[string]string{"workers": "env SAMPLE_TOOL_WORKERS", "sync.channels": "env SAMPLE_TOOL_SYNC_CHANNELS", "sync.interval": "default"},
		},
		{
			name: "both: the variable wins",
			file: `{"workers": 8, "sync": {"interval": "15m"}}`,
			env:  map[string]string{"SAMPLE_TOOL_WORKERS": "2"},
			check: func(t *testing.T, cfg sample) {
				if cfg.Workers != 2 || cfg.Sync.Interval != 15*time.Minute {
					t.Errorf("got %+v", cfg)
				}
			},
			source: map[string]string{"workers": "env SAMPLE_TOOL_WORKERS", "sync.interval": "file"},
		},
		{
			name: "explicit zero, false and empty in the file are kept over the defaults",
			file: `{"workers": 0, "dry_run": false, "name": "", "sync": {"interval": "0s"}}`,
			check: func(t *testing.T, cfg sample) {
				if cfg.Workers != 0 || cfg.DryRun || cfg.Sync.Interval != 0 {
					t.Errorf("got %+v", cfg)
				}
			},
			source: map[string]string{"workers": "file", "dry_run": "file", "name": "file", "sync.interval": "file"},
		},
		{
			name: "map from an object",
			file: `{"labels": {"team": "ops", "tier": "1"}}`,
			check: func(t *testing.T, cfg sample) {
				if cfg.Labels["team"] != "ops" || cfg.Labels["tier"] != "1" || len(cfg.Labels) != 2 {
					t.Errorf("got %+v", cfg.Labels)
				}
			},
		},
		{
			name: "null is not set",
			file: `{"workers": null, "sync": null}`,
			check: func(t *testing.T, cfg sample) {
				if cfg.Workers != 4 {
					t.Errorf("got %+v", cfg)
				}
			},
			source: map[string]string{"workers": "default"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup(t, tc.file)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			var cfg sample
			if err := Load(context.Background(), id, &cfg); err != nil {
				t.Fatal(err)
			}
			tc.check(t, cfg)
			got := settings(t)
			for key, want := range tc.source {
				if got[key]["source"] != want {
					t.Errorf("source of %s = %v, want %s", key, got[key]["source"], want)
				}
			}
		})
	}
}

func TestValueSetBeforeLoadIsKept(t *testing.T) {
	setup(t, "")
	cfg := sample{Workers: 2}
	if err := Load(context.Background(), id, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Workers != 2 {
		t.Errorf("Workers = %d", cfg.Workers)
	}
}

func TestExplicitEmptyClearsPresetValue(t *testing.T) {
	setup(t, `{"name": "", "sync": {"channels": []}}`)
	t.Setenv("SAMPLE_TOOL_TOKEN", "")
	cfg := sample{Name: "seed", Token: "seed", Sync: sync{Channels: []string{"seed"}}}
	if err := Load(context.Background(), id, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "" || cfg.Token != "" || len(cfg.Sync.Channels) != 0 {
		t.Errorf("got %+v", cfg)
	}
	got := settings(t)
	if got["name"]["source"] != "file" || got["token"]["source"] != "env SAMPLE_TOOL_TOKEN" || got["token"]["value"] != "unset" {
		t.Errorf("got %v", got)
	}
}

type secretGroup struct {
	Auth struct {
		User string `json:"user" env:"USER"`
		Pass string `json:"pass" env:"PASS"`
	} `json:"auth" env:", prefix=AUTH_" secret:"true"`
}

func TestPrefixOff(t *testing.T) {
	setup(t, "")
	t.Setenv("WORKERS", "3")
	t.Setenv("SAMPLE_TOOL_WORKERS", "7")
	var cfg sample
	if err := Load(context.Background(), id, &cfg, Prefix("")); err != nil {
		t.Fatal(err)
	}
	if cfg.Workers != 3 {
		t.Errorf("Workers = %d", cfg.Workers)
	}
	if settings(t)["workers"]["source"] != "env WORKERS" {
		t.Errorf("source = %v", settings(t)["workers"]["source"])
	}
}

type withRequired struct {
	Key string `json:"key" env:"KEY, required"`
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		env     map[string]string
		cfg     any
		class   failure.Class
		message []string
		next    []string
	}{
		{
			name:    "variable does not parse",
			env:     map[string]string{"SAMPLE_TOOL_WORKERS": "many"},
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"variable SAMPLE_TOOL_WORKERS", `"many"`, "invalid syntax"},
			next:    []string{"SAMPLE_TOOL_WORKERS"},
		},
		{
			name:    "nested variable does not parse",
			env:     map[string]string{"SAMPLE_TOOL_SYNC_INTERVAL": "soon"},
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"variable SAMPLE_TOOL_SYNC_INTERVAL", "invalid duration"},
		},
		{
			name:    "file value does not parse",
			file:    `{"sync": {"interval": "soon"}}`,
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"key sync.interval in ", "config.json", "invalid duration"},
			next:    []string{"sync.interval"},
		},
		{
			name:    "file value of the wrong shape",
			file:    `{"workers": {"n": 1}}`,
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"key workers in ", "expected a value, got an object"},
		},
		{
			name:    "list item with the delimiter",
			file:    `{"sync": {"channels": ["a,b"]}}`,
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"key sync.channels in ", `"a,b"`, "delimiter"},
		},
		{
			name:    "file is not JSON",
			file:    `{"workers": `,
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"config.json is not a JSON object"},
		},
		{
			name:    "file is a JSON list",
			file:    `[1]`,
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"config.json is not a JSON object"},
		},
		{
			name:    "file is null",
			file:    `null`,
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"config.json is not a JSON object: it is null"},
		},
		{
			name:    "content after the object",
			file:    `{} }`,
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"content after the JSON object"},
		},
		{
			name:    "unknown key and a group that is not an object",
			file:    `{"worker": 1, "sync": 5, "extra": {"x": 1}}`,
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"key extra is no setting; key sync must be an object; key worker is no setting"},
		},
		{
			name:    "unknown nested key",
			file:    `{"sync": {"intervals": "5m"}}`,
			cfg:     &sample{},
			class:   failure.Usage,
			message: []string{"key sync.intervals is no setting"},
		},
		{
			name:    "required setting missing",
			cfg:     &withRequired{},
			class:   failure.Usage,
			message: []string{"setting key is required"},
			next:    []string{"set key key in ", "config.json", "SAMPLE_TOOL_KEY"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup(t, tc.file)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			err := Load(context.Background(), id, tc.cfg)
			if err == nil {
				t.Fatal("no error")
			}
			var fe *failure.Error
			if !errors.As(err, &fe) || fe.Class != tc.class {
				t.Fatalf("error %v, want class %s", err, tc.class)
			}
			for _, part := range tc.message {
				if !strings.Contains(fe.Message, part) {
					t.Errorf("message %q lacks %q", fe.Message, part)
				}
			}
			for _, part := range tc.next {
				if !strings.Contains(fe.NextStep, part) {
					t.Errorf("next step %q lacks %q", fe.NextStep, part)
				}
			}
		})
	}
}

func TestRequiredFromFile(t *testing.T) {
	setup(t, `{"key": "k"}`)
	var cfg withRequired
	if err := Load(context.Background(), id, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Key != "k" {
		t.Errorf("Key = %q", cfg.Key)
	}
}

func TestMissingFileIsFine(t *testing.T) {
	setup(t, "")
	var cfg sample
	if err := Load(context.Background(), id, &cfg); err != nil {
		t.Fatal(err)
	}
}

type badDefault struct {
	N int `json:"n" env:"N, default=x"`
}

func TestBadDefaultIsInternal(t *testing.T) {
	setup(t, "")
	err := Load(context.Background(), id, &badDefault{})
	if failure.ClassOf(err) != failure.Internal || !strings.Contains(err.Error(), "default of setting n") {
		t.Errorf("got %v", err)
	}
}

func TestBadStructsPanic(t *testing.T) {
	cases := map[string]any{
		"json without env": &struct {
			A int `json:"a"`
		}{},
		"env without json": &struct {
			A int `env:"A"`
		}{},
		"unknown option": &struct {
			A int `json:"a" env:"A, nope"`
		}{},
		"bad variable name": &struct {
			A int `json:"a" env:"a-b"`
		}{},
		"required+default": &struct {
			A int `json:"a" env:"A, required, default=1"`
		}{},
		"prefix on a scalar": &struct {
			A int `json:"a" env:"A, prefix=X_"`
		}{},
		"struct with a name": &struct {
			S sync `json:"s" env:"S, prefix=S_"`
		}{},
		"no variable name": &struct {
			A int `json:"a" env:", required"`
		}{},
		"not a pointer": sample{},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			setup(t, "")
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			_ = Load(context.Background(), id, cfg)
		})
	}
}

type outer struct {
	Inner
	Own *string `json:"own" env:"OWN"`
	Sub *sync   `json:"sub" env:", prefix=SUB_"`
}

type Inner struct {
	Base string `json:"base" env:"BASE, default=b"`
}

func TestEmbeddedAndPointers(t *testing.T) {
	setup(t, `{"base": "f", "own": "o", "sub": {"interval": "1h"}}`)
	var cfg outer
	if err := Load(context.Background(), id, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Base != "f" || cfg.Own == nil || *cfg.Own != "o" || cfg.Sub == nil || cfg.Sub.Interval != time.Hour {
		t.Errorf("got %+v", cfg)
	}
	got := settings(t)
	if got["base"]["value"] != "f" || got["own"]["value"] != "o" || got["sub.interval"]["value"] != "1h0m0s" {
		t.Errorf("got %v", got)
	}
	setup(t, "")
	var empty outer
	if err := Load(context.Background(), id, &empty); err != nil {
		t.Fatal(err)
	}
	got = settings(t)
	if got["own"]["value"] != "" || got["sub.interval"]["value"] != "5m0s" || got["base"]["value"] != "b" {
		t.Errorf("got %v", got)
	}
}
