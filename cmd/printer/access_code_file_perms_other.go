//go:build !unix

package printer

import "os"

func accessCodeFileHasInsecurePermissions(_ os.FileMode) bool {
	return false
}
