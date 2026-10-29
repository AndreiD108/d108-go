package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AndreiD108/d108-go/doctor"
	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
)

var id = identity.Identity{Name: "sample-tool", Version: "dev", InstanceID: "01TEST", PID: 1}

// setup gives the test its own Config directory and a fixed machine id.
func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SAMPLE_TOOL_CONFIG_DIR", dir)
	setMachine(t, machineID{Source: sourceLinux, Value: "machine-a"})
	return dir
}

func setMachine(t *testing.T, m machineID) {
	t.Helper()
	old := readMachine
	readMachine = func() (machineID, error) { return m, nil }
	t.Cleanup(func() { readMachine = old })
}

func TestRoundTrip(t *testing.T) {
	dir := setup(t)
	s := Open(id)
	if err := s.Save("token", "t-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("other", "o-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("token", "t-2"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"token": "t-2", "other": "o-1"} {
		got, err := s.Load(name)
		if err != nil || got != want {
			t.Errorf("Load(%s) = %q, %v; want %q", name, got, err, want)
		}
	}
	names, err := s.Names()
	if err != nil || strings.Join(names, ",") != "other,token" {
		t.Errorf("Names = %v, %v", names, err)
	}
	for _, f := range []string{FileName, KeyFileName} {
		info, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %o", f, info.Mode().Perm())
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("t-2")) || bytes.Contains(data, []byte("token")) {
		t.Error("the file holds a name or a value in clear")
	}
	// A second Store over the same directory reads what the first wrote.
	got, err := Open(id).Load("other")
	if err != nil || got != "o-1" {
		t.Errorf("second store: %q, %v", got, err)
	}
}

func TestDeleteAndUnknown(t *testing.T) {
	setup(t)
	s := Open(id)
	if _, err := s.Load("none"); failure.ClassOf(err) != failure.NotFound {
		t.Errorf("Load on an empty store: %v", err)
	}
	if err := s.Save("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("a"); err != nil {
		t.Errorf("second Delete: %v", err)
	}
	if _, err := s.Load("a"); failure.ClassOf(err) != failure.NotFound {
		t.Errorf("after Delete: %v", err)
	}
	names, err := s.Names()
	if err != nil || len(names) != 0 {
		t.Errorf("Names = %v, %v", names, err)
	}
	if err := s.Save("", "x"); failure.ClassOf(err) != failure.Usage {
		t.Errorf("empty name: %v", err)
	}
}

func TestOtherMachineIsAuth(t *testing.T) {
	dir := setup(t)
	if err := Open(id).Save("token", "t"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]machineID{
		"other id":       {Source: sourceLinux, Value: "machine-b"},
		"other platform": {Source: sourceMacOS, Value: "machine-a"},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			setMachine(t, m)
			s := Open(id)
			_, err := s.Load("token")
			if failure.ClassOf(err) != failure.Auth || !strings.Contains(err.Error(), "cannot be read on this machine") {
				t.Errorf("Load: %v", err)
			}
			if err := s.Save("token", "t2"); failure.ClassOf(err) != failure.Auth {
				t.Errorf("Save: %v", err)
			}
		})
	}
	t.Run("key file lost", func(t *testing.T) {
		if err := os.Remove(filepath.Join(dir, KeyFileName)); err != nil {
			t.Fatal(err)
		}
		_, err := Open(id).Load("token")
		if failure.ClassOf(err) != failure.Auth || !strings.Contains(err.Error(), "key file is missing") {
			t.Errorf("Load: %v", err)
		}
	})
}

func TestDamagedFileIsAuth(t *testing.T) {
	dir := setup(t)
	s := Open(id)
	if err := s.Save("token", "t"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	for name, content := range map[string]string{
		"not json":      "{",
		"other version": `{"version":2,"kdf":"hkdf-sha256","cipher":"aes-256-gcm","machine_id_source":"linux-machine-id","nonce":"AAAAAAAAAAAAAAAA","ciphertext":"AA=="}`,
		"bad nonce":     `{"version":1,"kdf":"hkdf-sha256","cipher":"aes-256-gcm","machine_id_source":"linux-machine-id","nonce":"AA==","ciphertext":"AA=="}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := s.Load("token")
			if failure.ClassOf(err) != failure.Auth || !errors.Is(err, errFormat) {
				t.Errorf("Load: %v", err)
			}
		})
	}
	t.Run("ciphertext changed", func(t *testing.T) {
		// Save refuses a file it cannot open, so start the file again.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := Open(id).Save("token", "t"); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatal(err)
		}
		ct, _ := base64.StdEncoding.DecodeString(env.Ciphertext)
		ct[0] ^= 1
		env.Ciphertext = base64.StdEncoding.EncodeToString(ct)
		data, _ = json.Marshal(env)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = s.Load("token")
		if failure.ClassOf(err) != failure.Auth || !errors.Is(err, errOpen) {
			t.Errorf("Load: %v", err)
		}
	})
}

func TestConcurrentSaves(t *testing.T) {
	setup(t)
	s := Open(id)
	var wg sync.WaitGroup
	for g := range 2 {
		wg.Go(func() {
			for i := range 20 {
				if err := s.Save("k", strings.Repeat("ab", g+i)); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	v, err := s.Load("k")
	if err != nil || !strings.HasPrefix(v, "ab") || len(v)%2 != 0 {
		t.Errorf("after the saves: %q, %v", v, err)
	}
}

// TestFirstSavesFromTwoStores: two stores (two processes) saving into an empty directory at
// once must end with one key file that opens the secrets file, whichever write came last.
func TestFirstSavesFromTwoStores(t *testing.T) {
	for range 50 {
		setup(t)
		a, b := Open(id), Open(id)
		var wg sync.WaitGroup
		for i, s := range []*Store{a, b} {
			wg.Go(func() {
				if err := s.Save("k", strings.Repeat("x", i+1)); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		v, err := Open(id).Load("k")
		if err != nil || (v != "x" && v != "xx") {
			t.Fatalf("after two first saves: %q, %v", v, err)
		}
	}
}

func TestFailedFirstSavePublishesNoKey(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := setup(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	err := Open(id).Save("k", "v")
	if failure.ClassOf(err) != failure.Permission {
		t.Errorf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("files left after the failed save: %v", entries)
	}
}

func TestDoctorSection(t *testing.T) {
	setup(t)
	s := Open(id)
	section := func() map[string]map[string]any {
		data, err := json.Marshal(doctor.Build(context.Background(), id))
		if err != nil {
			t.Fatal(err)
		}
		var report map[string]map[string]map[string]any
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		return report["secrets"]
	}
	if got := section(); got["stored"]["value"] != "none" {
		t.Errorf("empty: %v", got)
	}
	if err := s.Save("token", "t"); err != nil {
		t.Fatal(err)
	}
	got := section()
	if got["token"]["value"] != "stored" || len(got) != 1 {
		t.Errorf("one secret: %v", got)
	}
	if _, has := got["token"]["note"]; has {
		t.Errorf("value leaked into the report: %v", got)
	}
	setMachine(t, machineID{Source: sourceLinux, Value: "machine-b"})
	got = section()
	if got["error"] == nil || got["error"]["note"] != "enter the secrets again" {
		t.Errorf("other machine: %v", got)
	}
}

// TestFixture opens the contract's envelope: the bytes every d108 library must read.
func TestFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		KeyFile         string            `json:"key_file"`
		MachineIDSource string            `json:"machine_id_source"`
		MachineID       string            `json:"machine_id"`
		Envelope        string            `json:"envelope"`
		Values          map[string]string `json:"values"`
	}
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatal(err)
	}
	dir := setup(t)
	setMachine(t, machineID{Source: fx.MachineIDSource, Value: fx.MachineID})
	keyFile, err := base64.StdEncoding.DecodeString(fx.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, KeyFileName), keyFile, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(fx.Envelope), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(id)
	for name, want := range fx.Values {
		got, err := s.Load(name)
		if err != nil || got != want {
			t.Errorf("Load(%s) = %q, %v; want %q", name, got, err, want)
		}
	}
	if source, err := os.ReadFile("../../d108/golden/secrets.json"); err == nil && !bytes.Equal(source, data) {
		t.Error("testdata/secrets.json differs from the contract repo; run make sync-contract")
	}
}
