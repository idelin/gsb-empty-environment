package netsec

// PIDNamespace represents a PID namespace in the sandboxed process tree.
//
// Namespaces are organized as a forest: a namespace has at most one parent and
// every namespace can see its own processes plus every process of its ancestor
// namespaces. A sibling namespace or a descendant namespace is not visible.
// Namespace identity is pointer identity; two *PIDNamespace values describe the
// same namespace only if they point to the same struct.
type PIDNamespace struct {
	name   string
	parent *PIDNamespace
}

// NewPIDNamespace creates a namespace. parent may be nil for a root namespace.
func NewPIDNamespace(name string, parent *PIDNamespace) *PIDNamespace {
	return &PIDNamespace{name: name, parent: parent}
}

// Name returns the diagnostic name of the namespace.
func (n *PIDNamespace) Name() string { return n.name }

// Sees reports whether a process living in target is visible from a caller
// located in n.
//
// Following Linux PID-namespace semantics, a namespace sees itself and every
// descendant namespace: outer namespaces can look into inner ones. A process in
// an outer (ancestor) namespace is invisible from an inner namespace, and
// sibling namespaces cannot see each other at all.
func (n *PIDNamespace) Sees(target *PIDNamespace) bool {
	if n == nil {
		return false
	}
	for cur := target; cur != nil; cur = cur.parent {
		if cur == n {
			return true
		}
	}
	return false
}

// Process is the host's snapshot of a sandboxed process at the time of an
// authorization decision. The host resolver fills it from its own process
// bookkeeping (for example /proc on Linux). The authorization package never
// touches real processes itself.
type Process struct {
	// ID is a stable host-wide identifier that is never reused while the
	// host runs. PIDs alone are not stable identifiers across reuse.
	ID string

	// PID is the process ID inside its own PID namespace.
	PID int

	// StartTime is the kernel start time of the process (typically the
	// starttime field of /proc/[pid]/stat, in clock ticks since boot).
	// Combined with PID it uniquely identifies one kernel process
	// incarnation and lets us tell a reused PID apart.
	StartTime uint64

	// UID is the real user ID that owns the process.
	UID int

	// NS is the PID namespace in which PID is allocated.
	NS *PIDNamespace

	// Alive reports whether the process has not exited. A zombie is treated
	// as alive by the kernel for reaping reasons, but a host that wants
	// zombies denied can report false here; the policy only requires that an
	// exited target yields ESRCH.
	Alive bool
}

// ProcessResolver is the host-side lookup the policy needs.
//
// LookupByPID returns the process currently occupying pid inside ns together
// with ok=true, or ok=false if no live process holds that PID there.
// Implementations must be safe for concurrent use.
type ProcessResolver interface {
	LookupByPID(ns *PIDNamespace, pid int) (Process, bool)
}

// Caller is the credential snapshot of the thread invoking
// pidfd_send_signal.
type Caller struct {
	// UID is the effective user ID of the caller.
	UID int

	// CapKill reports whether the caller holds CAP_KILL in the user
	// namespace that owns the target process.
	CapKill bool

	// NS is the PID namespace of the caller; the target must be visible
	// from it.
	NS *PIDNamespace

	// SelfID is the stable Process.ID of the caller itself. It is used to
	// recognize self-directed signals, which are exempt from the siginfo
	// si_code restrictions.
	SelfID string
}

// SigInfo is the subset of struct siginfo / kernel_siginfo inspected by the
// pidfd_send_signal policy. Field meanings match the Linux siginfo layout.
type SigInfo struct {
	// Signo is si_signo, the signal number carried inside siginfo.
	Signo int

	// Code is si_code. Positive/zero codes name a signal origin (e.g.
	// SI_USER, SI_QUEUE); negative codes are kernel-generated.
	Code int
}

// si_code values relevant to the siginfo spoofing check. Values mirror Linux.
const (
	siUser   = 0  // SI_USER: sent by kill/raise
	siQueue  = -1 // SI_QUEUE: sent by sigqueue
	siTkill  = -6 // SI_TKILL: sent by tkill/tgkill
	siKernel = -128
)

// MaxSignal is the highest signal number the policy accepts. Linux defines 64
// signals (32 standard + 32 real-time).
const MaxSignal = 64
