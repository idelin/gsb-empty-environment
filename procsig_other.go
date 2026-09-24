//go:build !linux

package hostlock

// processStartSig is unsupported on this platform; ownership safety never
// relies on it (the nonce and fencing token provide that), only the Live/Lost
// status display falls back to PID-existence alone.
func processStartSig(pid int) string { return "" }
