package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/AndreiD108/d108-go/failure"
)

// ReadSecret reads one line from stdin without echo, for a key or a password. When stdin is
// a terminal it writes prompt to Stderr(ctx), turns echo off while the line is typed,
// restores the terminal and writes a newline, since Enter printed none. When stdin is not a
// terminal it returns the first line of stdin, without a prompt. The newline is not part of
// the result. A cancelled ctx ends the read with ctx.Err() and the terminal restored, so a
// prompt under lifecycle.Run ends on Ctrl+C. End of input before any byte is a usage failure.
func ReadSecret(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, ok := Stdin(ctx).(*os.File)
	if !ok {
		return firstLineOrCancel(ctx, Stdin(ctx))
	}
	in, release, err := pollable(f)
	if err != nil {
		return "", failure.Wrap(err, failure.Internal, "cannot prepare stdin for a prompt: "+err.Error(), "pipe the secret on stdin instead")
	}
	defer release()
	stop := context.AfterFunc(ctx, func() { _ = in.SetReadDeadline(time.Now()) })
	defer stop()
	restore, err := quiet(in)
	if err != nil {
		return "", failure.Wrap(err, failure.Internal, "cannot turn echo off: "+err.Error(), "pipe the secret on stdin instead")
	}
	if restore != nil {
		defer func() {
			restore()
			_, _ = io.WriteString(Stderr(ctx), "\n")
		}()
		if _, err := io.WriteString(Stderr(ctx), prompt); err != nil {
			return "", err
		}
	}
	return firstLine(ctx, in)
}

// firstLineOrCancel is firstLine for a reader that is not a file, so no deadline can end
// its Read: the read runs in a goroutine and a cancelled ctx returns without waiting for it.
// The goroutine ends when the reader yields; its result is then dropped.
func firstLineOrCancel(ctx context.Context, r io.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := firstLine(ctx, r)
		done <- result{line, err}
	}()
	select {
	case res := <-done:
		return res.line, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// firstLine reads r one byte at a time up to the first newline, so nothing past the line is
// consumed, and returns the line without it. A read that ended because ctx was cancelled
// returns ctx.Err(); end of input before any byte is a usage failure.
func firstLine(ctx context.Context, r io.Reader) (string, error) {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return string(line), nil
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				if len(line) == 0 {
					return "", failure.New(failure.Usage, "no secret was given", "type the secret at the prompt, or pipe one line on stdin")
				}
				return string(line), nil
			}
			return "", err
		}
	}
}

// pollable returns a duplicate of f that Go's poller watches, so SetReadDeadline ends a
// blocked read, with the function that undoes it. O_NONBLOCK belongs to the open file
// description f and the duplicate share, so the duplicate sets it on f too; release puts
// it back as it was and closes the duplicate. f itself stays open and is not read.
func pollable(f *os.File) (*os.File, func(), error) {
	conn, err := f.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var dup int
	var wasBlocking bool
	if cerr := conn.Control(func(fd uintptr) {
		flags, e := unix.FcntlInt(fd, unix.F_GETFL, 0)
		if e != nil {
			err = e
			return
		}
		wasBlocking = flags&unix.O_NONBLOCK == 0
		if dup, err = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0); err != nil {
			return
		}
		if err = unix.SetNonblock(dup, true); err != nil {
			_ = unix.Close(dup)
		}
	}); cerr != nil {
		return nil, nil, cerr
	}
	if err != nil {
		return nil, nil, err
	}
	in := os.NewFile(uintptr(dup), f.Name())
	release := func() {
		_ = in.Close()
		if wasBlocking {
			_ = conn.Control(func(fd uintptr) { _ = unix.SetNonblock(int(fd), false) })
		}
	}
	return in, release, nil
}

// quiet turns echo off on the terminal f is open on and returns the function that puts the
// attributes back. The line discipline stays canonical, with signals and CR to NL, so Enter
// ends the line and Ctrl+C still interrupts. When f is not a terminal (ENOTTY: a pipe, a
// file, a socket) it returns nil, nil; any other failure is an error.
func quiet(f *os.File) (restore func(), err error) {
	conn, err := f.SyscallConn()
	if err != nil {
		return nil, err
	}
	var saved *unix.Termios
	if cerr := conn.Control(func(fd uintptr) {
		if saved, err = unix.IoctlGetTermios(int(fd), ioctlReadTermios); err != nil {
			return
		}
		attrs := *saved
		attrs.Lflag &^= unix.ECHO
		attrs.Lflag |= unix.ICANON | unix.ISIG
		attrs.Iflag |= unix.ICRNL
		err = unix.IoctlSetTermios(int(fd), ioctlWriteTermios, &attrs)
	}); cerr != nil {
		return nil, cerr
	}
	if errors.Is(err, unix.ENOTTY) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return func() {
		_ = conn.Control(func(fd uintptr) { _ = unix.IoctlSetTermios(int(fd), ioctlWriteTermios, saved) })
	}, nil
}
