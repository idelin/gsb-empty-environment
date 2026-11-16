//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package netsec

import (
	"fmt"
	"runtime"
)

func fileOwnerUID(path string) (uint32, error) {
	return 0, fmt.Errorf("netsec: file ownership is unsupported on %s", runtime.GOOS)
}
