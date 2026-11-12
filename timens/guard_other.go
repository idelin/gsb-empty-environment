//go:build !linux

package timens

import "io"

// UnsupportedGuard is the NamespaceGuard implementation on platforms without
// time namespaces. Every operation fails with ErrUnsupportedPlatform.
type UnsupportedGuard struct{}

// Empty always returns ErrUnsupportedPlatform off Linux.
func (UnsupportedGuard) Empty(string) error { return ErrUnsupportedPlatform }

// UnsupportedCapChecker is the CapabilityChecker implementation on platforms
// without Linux capability sets.
type UnsupportedCapChecker struct{}

// HasSysTime always returns ErrUnsupportedPlatform off Linux.
func (UnsupportedCapChecker) HasSysTime() (bool, error) {
	return false, ErrUnsupportedPlatform
}

// WriteProcOffsets is unavailable on non-Linux platforms.
func WriteProcOffsets(pid int, r io.Reader, hostMonotonic HostTime) ([]Warning, error) {
	return nil, ErrUnsupportedPlatform
}
