//go:build unix

package printer

import "os"

func accessCodeFileHasInsecurePermissions(mode os.FileMode) bool {
	return mode.Perm()&0077 != 0
}
