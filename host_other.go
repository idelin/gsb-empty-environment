//go:build !linux

package netsec

type unsupportedHost struct{}

// NewOSHost returns a Host that reports ErrUnsupported.  Use NewMemoryHost
// when the authorization package is being embedded in a simulator.
func NewOSHost() Host { return unsupportedHost{} }

func (unsupportedHost) LookupProcess(int) (ProcessInfo, error) {
	return ProcessInfo{}, ErrUnsupported
}

func (unsupportedHost) SendSignal(int, int, int, *Siginfo, uint32) error {
	return ErrUnsupported
}

// PidfdOpen reports ErrUnsupported outside Linux.
func PidfdOpen(int) (int, error) { return -1, ErrUnsupported }

// OpenOSPIDFD is an alias for PidfdOpen.
func OpenOSPIDFD(pid int) (int, error) { return PidfdOpen(pid) }
