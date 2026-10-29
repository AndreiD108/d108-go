package cli

import "golang.org/x/sys/unix"

// The ioctl requests that read and write the terminal attributes.
const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)
