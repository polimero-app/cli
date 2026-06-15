//go:build !unix

package printer

import "os"

func openAccessCodeFile(path string) (*os.File, error) {
	return os.Open(path)
}
