// Package atomicfile writes a file through a temp file and a rename, so a reader sees the
// old content or the new content, never a partial file.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write writes data to path with mode. The parent directory is created with mode 0700 when
// it does not exist; the temp file is written, synced, closed and renamed over path. On any
// failure the temp file is removed and path is untouched.
func Write(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
