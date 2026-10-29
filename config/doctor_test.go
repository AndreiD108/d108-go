package config

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AndreiD108/d108-go/doctor"
	"github.com/AndreiD108/d108-go/failure"
)

func settings(t *testing.T) map[string]map[string]any {
	t.Helper()
	data, err := json.Marshal(doctor.Build(context.Background(), id))
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]map[string]map[string]any
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report["settings"]
}

func TestSecretGroupHidesChildren(t *testing.T) {
	setup(t, `{"auth": {"user": "u"}}`)
	if err := Load(context.Background(), id, &secretGroup{}); err != nil {
		t.Fatal(err)
	}
	got := settings(t)
	if got["auth.user"]["value"] != "set" || got["auth.pass"]["value"] != "unset" {
		t.Errorf("got %v", got)
	}
}

func TestDoctorItems(t *testing.T) {
	setup(t, `{"token": "s3cret", "labels": {"b": "2", "a": "1"}, "sync": {"channels": ["general", "ops"]}}`)
	var cfg sample
	if err := Load(context.Background(), id, &cfg); err != nil {
		t.Fatal(err)
	}
	got := settings(t)
	for key, want := range map[string][2]string{
		"token":         {"set", "file"},
		"labels":        {"a=1, b=2", "file"},
		"sync.channels": {"general, ops", "file"},
		"sync.interval": {"5m0s", "default"},
		"workers":       {"4", "default"},
		"dry_run":       {"true", "default"},
	} {
		if got[key]["value"] != want[0] || got[key]["source"] != want[1] {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	if _, ok := got["ignored"]; ok {
		t.Error("a field without tags is listed")
	}
	if len(got) != 7 {
		t.Errorf("%d items, want 7: %v", len(got), got)
	}
	// The snapshot does not follow later writes to the struct.
	cfg.Workers = 9
	if settings(t)["workers"]["value"] != "4" {
		t.Error("doctor follows the struct after Load")
	}
}

func TestSecretUnset(t *testing.T) {
	setup(t, "")
	var cfg sample
	if err := Load(context.Background(), id, &cfg); err != nil {
		t.Fatal(err)
	}
	if settings(t)["token"]["value"] != "unset" {
		t.Errorf("token = %v", settings(t)["token"])
	}
}

func TestLoadFailureKeepsTheDoctorSection(t *testing.T) {
	setup(t, `{"workers": 2, "name": "x", "dry_run": "yes"}`)
	var cfg sample
	err := Load(context.Background(), id, &cfg)
	if !failure.Is(err, failure.Usage) {
		t.Fatalf("err = %v", err)
	}
	got := settings(t)
	if got["workers"]["value"] != "2" || got["workers"]["source"] != "file" || got["name"]["value"] != "x" {
		t.Errorf("resolved settings %v", got)
	}
	if _, ok := got["dry_run"]; ok {
		t.Error("the failed setting is listed")
	}
	if _, ok := got["token"]; ok {
		t.Error("a setting after the failed one is listed")
	}
	if got["error"]["value"] != failure.From(err).Message || got["error"]["note"] != failure.From(err).NextStep {
		t.Errorf("error item %v", got["error"])
	}

	setup(t, `[1]`)
	err = Load(context.Background(), id, &cfg)
	if err == nil {
		t.Fatal("a file that is not an object must fail")
	}
	got = settings(t)
	if len(got) != 1 || got["error"]["value"] != failure.From(err).Message {
		t.Errorf("settings %v, want the error item alone", got)
	}
}
