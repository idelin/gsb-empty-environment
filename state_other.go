//go:build !linux

package netsec

import (
	"fmt"
	"runtime"
)

func readState(path string) (FileState, error) {
	return FileState{}, fmt.Errorf("%w: security.evm file evaluation on %s", ErrUnsupported, runtime.GOOS)
}
