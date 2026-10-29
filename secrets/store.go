// Package secrets keeps an app's secrets (standard.md section 9.5): API tokens and anything
// else that may not sit in plain text. One file, `<Config>/secrets.enc`, holds every name
// and value, encrypted with AES-256-GCM under a key derived with HKDF-SHA256 from a random
// key file next to it (`<Config>/secrets.key`, made on the first Save) and this machine's
// id (`/etc/machine-id` on Linux, `IOPlatformUUID` on macOS). Both files are 0600 and
// written through atomicfile, so a reader sees the old file or the new one. The two files
// copied to another machine do not open there: the user enters the secrets again.
//
// Open registers the doctor section "secrets" with the names only, never the values.
package secrets

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/AndreiD108/d108-go/atomicfile"
	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/paths"
)

// The two files inside the Config directory.
const (
	FileName    = "secrets.enc"
	KeyFileName = "secrets.key"
)

// Store is the app's secrets file. Calls on one Store are serialized; two processes
// writing the same file both write a whole file, and the last one wins.
type Store struct {
	mu   sync.Mutex
	file string
	key  string
}

// Open gives the store of id's Config directory and registers its doctor section. It
// reads nothing until the first call.
func Open(id identity.Identity) *Store {
	dir := paths.For(id.Name, paths.Prefix(id.EnvPrefix())).Config
	s := &Store{file: filepath.Join(dir, FileName), key: filepath.Join(dir, KeyFileName)}
	s.registerDoctor()
	return s
}

// Save stores value under name, replacing an older value. The first Save makes the key
// file. An empty name is a usage failure.
func (s *Store) Save(name, value string) error {
	if name == "" {
		return failure.New(failure.Usage, "a secret needs a name", "give the secret a name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values, keyFile, machine, err := s.read()
	if err != nil {
		return err
	}
	if keyFile == nil {
		if keyFile, err = s.createKey(); err != nil {
			return err
		}
	}
	values[name] = value
	return s.write(values, keyFile, machine)
}

// createKey makes the key file so that two processes on their first Save end with one key:
// the bytes go to a temp file, which is linked to the final name; link fails when the name
// exists, and then the loser reads the winner's complete file. A key file written over
// another would leave the other's secrets file unreadable.
func (s *Store) createKey() ([]byte, error) {
	keyFile := make([]byte, keyFileSize)
	if _, err := rand.Read(keyFile); err != nil {
		return nil, failure.Wrap(err, failure.Internal, "cannot make the key file: "+err.Error(), "retry")
	}
	dir := filepath.Dir(s.key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fsFailure(err, s.key)
	}
	tmp, err := os.CreateTemp(dir, "."+KeyFileName+".*.tmp")
	if err != nil {
		return nil, fsFailure(err, s.key)
	}
	defer os.Remove(tmp.Name())
	if err := writeAll(tmp, keyFile); err != nil {
		return nil, fsFailure(err, s.key)
	}
	err = os.Link(tmp.Name(), s.key)
	if errors.Is(err, os.ErrExist) {
		existing, err := os.ReadFile(s.key)
		if err != nil {
			return nil, fsFailure(err, s.key)
		}
		return existing, nil
	}
	if err != nil {
		return nil, fsFailure(err, s.key)
	}
	return keyFile, nil
}

// writeAll sets the mode, writes data, syncs and closes f; the first error wins and the
// file is closed on every path.
func writeAll(f *os.File, data []byte) error {
	err := f.Chmod(0o600)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// Load returns the value stored under name; an unknown name is a not_found failure.
func (s *Store) Load(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, _, _, err := s.read()
	if err != nil {
		return "", err
	}
	v, ok := values[name]
	if !ok {
		return "", failure.New(failure.NotFound, "no secret named "+name, "store it first", "name", name)
	}
	return v, nil
}

// Delete removes name. A name that is not stored is not an error.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, keyFile, machine, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := values[name]; !ok {
		return nil
	}
	delete(values, name)
	return s.write(values, keyFile, machine)
}

// Names lists the stored names, sorted.
func (s *Store) Names() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, _, _, err := s.read()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// read loads both files. Without a secrets file it returns no values; the key file is nil
// when it does not exist yet. A secrets file the key does not open is an auth failure: the
// files came from another machine, or the key file is gone.
func (s *Store) read() (map[string]string, []byte, machineID, error) {
	machine, err := readMachine()
	if err != nil {
		return nil, nil, machineID{}, failure.Wrap(err, failure.Internal, "cannot read the machine id: "+err.Error(),
			"report this to the tool's author")
	}
	// The secrets file is read before the key file: a writer publishes the key first, so a
	// secrets file seen here has its key on disk by the time the key is read.
	data, err := os.ReadFile(s.file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, machine, fsFailure(err, s.file)
	}
	hasFile := err == nil
	keyFile, err := os.ReadFile(s.key)
	if errors.Is(err, os.ErrNotExist) {
		keyFile = nil
	} else if err != nil {
		return nil, nil, machine, fsFailure(err, s.key)
	}
	if !hasFile {
		return map[string]string{}, keyFile, machine, nil
	}
	if keyFile == nil {
		return nil, nil, machine, unreadable(errors.New("the key file is missing"), s.file)
	}
	values, err := open(data, keyFile, machine)
	if err != nil {
		return nil, nil, machine, unreadable(err, s.file)
	}
	return values, keyFile, machine, nil
}

func (s *Store) write(values map[string]string, keyFile []byte, machine machineID) error {
	data, err := seal(values, keyFile, machine)
	if err != nil {
		return failure.Wrap(err, failure.Internal, "cannot encrypt the secrets: "+err.Error(), "retry")
	}
	if err := atomicfile.Write(s.file, data, 0o600); err != nil {
		return fsFailure(err, s.file)
	}
	return nil
}

func unreadable(cause error, file string) error {
	return failure.Wrap(cause, failure.Auth,
		fmt.Sprintf("stored secrets cannot be read on this machine (%s): %v", file, cause),
		"enter the secrets again", "file", file)
}

func fsFailure(err error, file string) error {
	class := failure.Internal
	if errors.Is(err, os.ErrPermission) {
		class = failure.Permission
	}
	return failure.Wrap(err, class, fmt.Sprintf("cannot access %s: %v", file, err),
		"check the file and its directory", "file", file)
}
