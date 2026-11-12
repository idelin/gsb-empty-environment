//go:build !linux

package netsec

import (
	"errors"
	"os"
)

var errUnsupportedPlatform = errors.New("遮蔽挂载仅支持 Linux")

func defaultOps() Ops {
	return Ops{
		Lstat: os.Lstat,
		BindMount: func(_, _ string) error {
			return errUnsupportedPlatform
		},
		MountReadonlyTmpfs: func(_ string) error {
			return errUnsupportedPlatform
		},
		RemountReadonly: func(_ string) error {
			return errUnsupportedPlatform
		},
		DevNull: func() (uint32, uint32, error) {
			return 0, 0, errUnsupportedPlatform
		},
	}
}
