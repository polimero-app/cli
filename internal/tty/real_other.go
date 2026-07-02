//go:build !unix

package tty

import "os"

// reraise approximates signal-death exit on platforms without syscall.Kill.
func reraise(os.Signal) {
	os.Exit(1)
}
