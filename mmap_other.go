//go:build !linux && !darwin

package netsec

import "errors"

// ErrMmapUnsupported 在不支持 mmap 辅助封装的平台上返回。
var ErrMmapUnsupported = errors.New("netsec: mmap helper is not supported on this platform; implement the Page interface instead")
