package secrets

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// hostMachineID reads IOPlatformUUID from ioreg, the hardware UUID macOS keeps for the
// machine.
func hostMachineID() (machineID, error) {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return machineID{}, fmt.Errorf("read the machine id with ioreg: %w", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if !strings.Contains(line, `"IOPlatformUUID"`) {
			continue
		}
		_, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v, err := normalize(strings.Trim(strings.TrimSpace(value), `"`))
		if err != nil {
			return machineID{}, err
		}
		return machineID{Source: sourceMacOS, Value: v}, nil
	}
	return machineID{}, errors.New("ioreg printed no IOPlatformUUID")
}
