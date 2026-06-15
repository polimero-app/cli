//go:build unix

package printer

import (
	"os"
	"syscall"
)

func openAccessCodeFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
