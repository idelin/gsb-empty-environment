//go:build !darwin && !linux

package appendhandle

// punchHole is unsupported on this platform. OSFile falls back to zeroing the
// range, which preserves every read-back guarantee except block deallocation.
func punchHole(fd uintptr, off, length int64) error { return errHoleUnsupported }

func holeUnsupported(err error) bool { return true }
