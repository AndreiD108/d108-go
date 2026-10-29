package secrets

import (
	"errors"
	"strings"
)

// machineID is the identity of this machine that the key derivation binds the secrets to.
// Source names where it came from and is stored in the envelope; Value is the id itself.
type machineID struct {
	Source string
	Value  string
}

// The source labels, one per platform. They are part of the file format: a library in
// another language must use the same strings.
const (
	sourceLinux = "linux-machine-id"
	sourceMacOS = "macos-ioplatformuuid"
)

// readMachine reads this machine's id. Tests replace it to run the scheme on a known id.
var readMachine = hostMachineID

// normalize trims the whitespace and NUL bytes a file or a command adds around the id.
func normalize(raw string) (string, error) {
	v := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "\x00"))
	if v == "" {
		return "", errors.New("machine id is empty")
	}
	return v, nil
}
