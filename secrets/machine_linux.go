package secrets

import (
	"fmt"
	"os"
)

// hostMachineID reads /etc/machine-id, which systemd sets at install and keeps stable.
func hostMachineID() (machineID, error) {
	raw, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return machineID{}, fmt.Errorf("read the machine id: %w", err)
	}
	v, err := normalize(string(raw))
	if err != nil {
		return machineID{}, err
	}
	return machineID{Source: sourceLinux, Value: v}, nil
}
