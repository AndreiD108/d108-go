package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesDirAndSetsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "secrets.enc")
	if err := Write(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "one" {
		t.Fatalf("read back %q, %v", got, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(filepath.Dir(path))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o", dirInfo.Mode().Perm())
	}
	if err := Write(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "two" {
		t.Errorf("second write: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestWriteLeavesTargetOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Write(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if os.Getuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	if err := Write(path, []byte("new"), 0o600); err == nil {
		t.Fatal("expected an error on a read-only directory")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "keep" {
		t.Errorf("target changed: %q", got)
	}
}
