//go:build unix

package tty

import (
	"os"
	"syscall"
)

// reraise re-delivers sig to the current process with default disposition so
// the exit status reflects the signal.
func reraise(sig os.Signal) {
	if s, ok := sig.(syscall.Signal); ok {
		_ = syscall.Kill(os.Getpid(), s)
		return
	}
	os.Exit(1)
}
