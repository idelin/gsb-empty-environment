// Package netsec provides authorization for pidfd_send_signal in a Go
// sandbox host.
//
// A PIDFD binding records the target PID, its process start timestamp, user
// IDs and PID namespace when pidfd_open is intercepted.  Every later send
// revalidates that snapshot: a missing process or a recycled PID produces
// ESRCH and delivery is never attempted.  The registry also enforces the
// caller's user/CAP_KILL permission, PID namespace visibility, the siginfo
// spoofing rules, and a zero flags value.
//
// Host is the platform boundary.  NewOSHost supplies a /proc and syscall
// implementation on Linux; NewMemoryHost is useful for tests and simulators.
package netsec
