//go:build !linux

package netsec

// NewDisabledRing is only implemented on Linux. On every other platform it
// returns ErrUnsupportedPlatform so that host code compiles everywhere while
// the io_uring integration stays confined to Linux builds.
func NewDisabledRing(entries uint, extraFlags SetupFlag) (*Ring, error) {
	return nil, ErrUnsupportedPlatform
}
